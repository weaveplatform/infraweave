package deps

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/wait"
)

// The @vcs Gitea's administrator, who owns every scenario's repositories.
const (
	giteaUser, giteaPassword = "infraweave", "infraweave-gitea" //nolint:gosec // a container that lives as long as one test binary
	giteaPort                = "3000/tcp"
)

// Gitea is the @vcs dependency: a Gitea server over HTTPS, under a CA the
// suite makes for the run.
//
// ref: decision 0016
type Gitea struct {
	// URL is Gitea's HTTPS base URL from the host.
	URL string
	// CAFile is the PEM file of the CA Gitea's certificate chains to, for a
	// process that reaches Gitea to trust; Client already trusts it.
	CAFile string
	// Client calls Gitea's API as the administrator.
	Client *http.Client
}

// Gitea returns the shared Gitea, starting it on first use.
func (d *Dependencies) Gitea(ctx context.Context) (*Gitea, error) {
	d.giteaOnce.Do(func() {
		d.gitea, d.giteaErr = d.startGitea(ctx)
		if d.giteaErr != nil {
			d.giteaErr = fmt.Errorf("@vcs: %w", d.giteaErr)
		}
	})
	return d.gitea, d.giteaErr
}

// startGitea keeps the CA and the key pair in a directory of their own,
// which outlives every scenario and goes with the container.
func (d *Dependencies) startGitea(ctx context.Context) (*Gitea, error) {
	dir, err := os.MkdirTemp("", "infraweave-gitea-")
	if err != nil {
		return nil, fmt.Errorf("pki dir: %w", err)
	}
	d.OnClose(func(context.Context) error { return os.RemoveAll(dir) })
	pki, err := newGiteaPKI(dir)
	if err != nil {
		return nil, err
	}
	tlsConfig := &tls.Config{RootCAs: pki.pool, MinVersion: tls.VersionTLS12}
	c, err := testcontainers.Run(ctx, ImageGitea,
		testcontainers.WithExposedPorts(giteaPort),
		testcontainers.WithFiles(
			// Gitea runs as git, so the key must be readable by others in
			// the container; it signs nothing outside this run.
			testcontainers.ContainerFile{
				HostFilePath:      pki.cert,
				ContainerFilePath: "/data/certs/cert.pem",
				FileMode:          0o644,
			},
			testcontainers.ContainerFile{
				HostFilePath:      pki.key,
				ContainerFilePath: "/data/certs/key.pem",
				FileMode:          0o644,
			},
		),
		testcontainers.WithEnv(map[string]string{
			"GITEA__security__INSTALL_LOCK":        "true",
			"GITEA__server__PROTOCOL":              "https",
			"GITEA__server__CERT_FILE":             "/data/certs/cert.pem",
			"GITEA__server__KEY_FILE":              "/data/certs/key.pem",
			"GITEA__server__HTTP_PORT":             "3000",
			"GITEA__server__DISABLE_SSH":           "true",
			"GITEA__database__DB_TYPE":             "sqlite3",
			"GITEA__service__DISABLE_REGISTRATION": "true",
			"GITEA__repository__DEFAULT_BRANCH":    "main",
			"GITEA__log__LEVEL":                    "Warn",
		}),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/api/healthz").
			WithPort(giteaPort).WithTLS(true, tlsConfig)),
	)
	if c != nil {
		d.OnClose(func(ctx context.Context) error { return c.Terminate(ctx) })
	}
	if err != nil {
		return nil, err //nolint:wrapcheck // Gitea names the failure
	}
	// The administrator is made inside the container: registration is off,
	// and the API creates users only for an administrator.
	code, out, err := c.Exec(ctx, []string{
		"gitea", "--config", "/data/gitea/conf/app.ini", "admin", "user", "create", "--admin",
		"--username", giteaUser, "--password", giteaPassword,
		"--email", giteaUser + "@example.invalid", "--must-change-password=false",
	}, tcexec.WithUser("git"), tcexec.Multiplexed())
	if err != nil {
		return nil, fmt.Errorf("admin user: %w", err)
	}
	if code != 0 {
		msg, _ := io.ReadAll(out)
		return nil, fmt.Errorf("%w: admin user: exited %d: %s", ErrExec, code, msg)
	}
	base, err := c.PortEndpoint(ctx, giteaPort, "https")
	if err != nil {
		return nil, fmt.Errorf("endpoint: %w", err)
	}
	return &Gitea{
		URL:    base,
		CAFile: pki.ca,
		Client: &http.Client{
			// Gitea's TLS listener speaks HTTP/2 to a client that offers
			// only HTTP/1.1, and a Transport with its own TLS config offers
			// HTTP/2 only when forced.
			Transport: &http.Transport{TLSClientConfig: tlsConfig, ForceAttemptHTTP2: true},
			Timeout:   30 * time.Second,
		},
	}, nil
}

// giteaPKI is the run's CA and Gitea's key pair, as PEM files.
type giteaPKI struct {
	ca, cert, key string
	pool          *x509.CertPool
}

// newGiteaPKI makes a CA and a server certificate it signs for the names
// the host reaches a mapped port by. ECDSA P-256 is approved under
// fips140=only, so the fips-only job runs the fixture unchanged.
func newGiteaPKI(dir string) (*giteaPKI, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("CA key: %w", err)
	}
	now := time.Now()
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "infraweave acceptance CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("CA: %w", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return nil, fmt.Errorf("CA: %w", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("server key: %w", err)
	}
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("server certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("server key: %w", err)
	}
	p := &giteaPKI{
		ca:   filepath.Join(dir, "gitea-ca.pem"),
		cert: filepath.Join(dir, "gitea-cert.pem"),
		key:  filepath.Join(dir, "gitea-key.pem"),
		pool: x509.NewCertPool(),
	}
	p.pool.AddCert(caCert)
	for path, block := range map[string]*pem.Block{
		p.ca:   {Type: "CERTIFICATE", Bytes: caDER},
		p.cert: {Type: "CERTIFICATE", Bytes: der},
		p.key:  {Type: "PRIVATE KEY", Bytes: keyDER},
	} {
		if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
			return nil, fmt.Errorf("write %s: %w", filepath.Base(path), err)
		}
	}
	return p, nil
}

// ErrGitea is returned when Gitea refuses a call the harness makes.
var ErrGitea = errors.New("gitea refused the call")

// CreateRepository creates the administrator's repository name with files
// committed to main in one commit, and returns that commit's SHA.
func (g *Gitea) CreateRepository(
	ctx context.Context,
	name string,
	files map[string]string,
) (string, error) {
	if err := g.call(ctx, http.MethodPost, "/user/repos",
		map[string]any{"name": name, "private": false, "default_branch": "main"}, nil); err != nil {
		return "", err
	}
	return g.Commit(ctx, name, "main", files, "seed")
}

// Commit writes files to branch of the repository name in one commit,
// creating each file or replacing it, and creating the branch from main
// when it does not exist, and returns the commit's SHA.
func (g *Gitea) Commit(
	ctx context.Context,
	name, branch string,
	files map[string]string,
	message string,
) (string, error) {
	exists, err := g.branchExists(ctx, name, branch)
	if err != nil {
		return "", err
	}
	body := map[string]any{"message": message, "branch": branch}
	// A new branch starts from main, so a file it changes exists there.
	base := branch
	if !exists {
		base = "main"
		body["branch"], body["new_branch"] = "main", branch
	}
	var ops []map[string]string
	for path, content := range files {
		sha, err := g.fileSHA(ctx, name, base, path)
		if err != nil {
			return "", err
		}
		op := "create"
		if sha != "" {
			op = "update"
		}
		ops = append(ops, map[string]string{
			"operation": op, "path": path, "sha": sha,
			"content": base64.StdEncoding.EncodeToString([]byte(content)),
		})
	}
	body["files"] = ops
	var resp struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := g.call(
		ctx,
		http.MethodPost,
		"/repos/"+giteaUser+"/"+name+"/contents",
		body,
		&resp,
	); err != nil {
		return "", err
	}
	return resp.Commit.SHA, nil
}

// Head is the SHA of the head of branch of the repository name.
func (g *Gitea) Head(ctx context.Context, name, branch string) (string, error) {
	var resp struct {
		Commit struct {
			ID string `json:"id"`
		} `json:"commit"`
	}
	if err := g.call(ctx, http.MethodGet,
		"/repos/"+giteaUser+"/"+name+"/branches/"+url.PathEscape(branch), nil, &resp); err != nil {
		return "", err
	}
	return resp.Commit.ID, nil
}

// Tag creates the annotated tag tag of commit in the repository name.
func (g *Gitea) Tag(ctx context.Context, name, tag, commit string) error {
	return g.call(ctx, http.MethodPost, "/repos/"+giteaUser+"/"+name+"/tags",
		map[string]any{"tag_name": tag, "target": commit, "message": tag}, nil)
}

// CloneURL is the HTTPS URL the repository name is cloned from.
func (g *Gitea) CloneURL(name string) string {
	return g.URL + "/" + giteaUser + "/" + name + ".git"
}

// Repository is the name of the repository CloneURL answers cloneURL for,
// empty for a URL it did not answer.
func (g *Gitea) Repository(cloneURL string) string {
	name, ok := strings.CutPrefix(cloneURL, g.URL+"/"+giteaUser+"/")
	if !ok {
		return ""
	}
	name, _ = strings.CutSuffix(name, ".git")
	return name
}

// Files are the files at the top of the repository name at ref, a commit
// or branch, by path: what a checkout of ref holds at its root.
func (g *Gitea) Files(ctx context.Context, name, ref string) (map[string]string, error) {
	base := "/repos/" + giteaUser + "/" + name + "/contents"
	var entries []struct {
		Path string `json:"path"`
		Type string `json:"type"`
	}
	if err := g.call(ctx, http.MethodGet, base+"?ref="+url.QueryEscape(ref), nil,
		&entries); err != nil {
		return nil, err
	}
	files := map[string]string{}
	for _, e := range entries {
		if e.Type != "file" {
			continue
		}
		var f struct {
			Content string `json:"content"`
		}
		if err := g.call(ctx, http.MethodGet, base+"/"+e.Path+"?ref="+url.QueryEscape(ref), nil,
			&f); err != nil {
			return nil, err
		}
		b, err := base64.StdEncoding.DecodeString(f.Content)
		if err != nil {
			return nil, fmt.Errorf("%w: %s at %s is not base64: %w", ErrGitea, e.Path, ref, err)
		}
		files[e.Path] = string(b)
	}
	return files, nil
}

func (g *Gitea) branchExists(ctx context.Context, name, branch string) (bool, error) {
	_, err := g.Head(ctx, name, branch)
	if errors.Is(err, errGiteaNotFound) {
		return false, nil
	}
	return err == nil, err
}

// fileSHA is the blob SHA of path at branch, empty when the branch or the
// file does not exist.
func (g *Gitea) fileSHA(ctx context.Context, name, branch, path string) (string, error) {
	var resp struct {
		SHA string `json:"sha"`
	}
	err := g.call(ctx, http.MethodGet, "/repos/"+giteaUser+"/"+name+"/contents/"+path+
		"?ref="+url.QueryEscape(branch), nil, &resp)
	if errors.Is(err, errGiteaNotFound) {
		return "", nil
	}
	return resp.SHA, err
}

var errGiteaNotFound = fmt.Errorf("%w: not found", ErrGitea)

func (g *Gitea) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("%s %s: %w", method, path, err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.URL+"/api/v1"+path, body)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	req.SetBasicAuth(giteaUser, giteaPassword)
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.Client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: %s %s", errGiteaNotFound, method, path)
	}
	if resp.StatusCode >= http.StatusMultipleChoices {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%w: %s %s: %s: %s", ErrGitea, method, path, resp.Status, msg)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	return nil
}
