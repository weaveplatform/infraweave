package deps

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
)

// EnvLabManifest selects an explicitly provisioned Terraform batch. An invalid
// manifest fails the suite; it must never silently start replacement services.
const EnvLabManifest = "INFRAWEAVE_LAB_MANIFEST"

// ErrLab identifies an invalid or unavailable Terraform-owned deployment.
var ErrLab = errors.New("terraform lab deployment")

type labManifest struct {
	Schema     int            `json:"schema"`
	Owner      string         `json:"owner"`
	Batch      string         `json:"batch"`
	Kubernetes *labKubernetes `json:"kubernetes,omitempty"`
	Postgres   struct {
		Container string `json:"container"`
		DSN       string `json:"dsn"`
	} `json:"postgres"`
	ObjectStore struct {
		Container string `json:"container"`
		Endpoint  string `json:"endpoint"`
	} `json:"objectstore"`
	Gitea struct {
		Container string `json:"container"`
		URL       string `json:"url"`
		CAFile    string `json:"caFile"`
	} `json:"gitea"`
}

var (
	labOwner     = regexp.MustCompile(`^iw-lab-[a-z0-9-]{1,60}$`)
	labContainer = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func readLab(data []byte) (*labManifest, error) {
	var manifest labManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("%w: malformed manifest", ErrLab)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("%w: trailing manifest data", ErrLab)
	}
	if manifest.Schema != 1 || !labOwner.MatchString(manifest.Owner) || manifest.Batch == "" {
		return nil, fmt.Errorf("%w: schema or ownership", ErrLab)
	}
	for _, id := range []string{manifest.Postgres.Container, manifest.ObjectStore.Container, manifest.Gitea.Container} {
		if !labContainer.MatchString(id) {
			return nil, fmt.Errorf("%w: container identity", ErrLab)
		}
	}
	for _, endpoint := range []struct{ address, scheme string }{
		{manifest.Postgres.DSN, "postgres"}, {manifest.ObjectStore.Endpoint, "http"}, {manifest.Gitea.URL, "https"},
	} {
		u, err := url.Parse(endpoint.address)
		if err != nil || u.Scheme != endpoint.scheme || u.Port() == "" || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() {
			return nil, fmt.Errorf("%w: expected local service endpoint", ErrLab)
		}
	}

	if !filepath.IsAbs(manifest.Gitea.CAFile) {
		return nil, fmt.Errorf("%w: absolute CA path required", ErrLab)
	}
	if cluster := manifest.Kubernetes; cluster != nil {
		endpoint, err := url.Parse(cluster.Endpoint)
		if !labContainer.MatchString(cluster.Container) || err != nil || endpoint.Scheme != "https" ||
			endpoint.User != nil || endpoint.Port() == "" || net.ParseIP(endpoint.Hostname()) == nil ||
			!net.ParseIP(endpoint.Hostname()).IsLoopback() || endpoint.Path != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" ||
			!filepath.IsAbs(cluster.Kubeconfig) || filepath.Dir(cluster.Kubeconfig) != filepath.Dir(manifest.Gitea.CAFile) ||
			cluster.Namespace != labKubernetesNamespace(manifest.Owner) {
			return nil, fmt.Errorf("%w: Kubernetes manifest identity", ErrLab)
		}
	}
	return &manifest, nil
}

type labCommand func(context.Context, ...string) ([]byte, error)

func dockerLab(ctx context.Context, args ...string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%w: Docker command: %w", ErrLab, err)
	}
	return output, nil
}

func verifyLab(ctx context.Context, m *labManifest, run labCommand) error {
	services := map[string]string{"postgres": m.Postgres.Container, "objectstore": m.ObjectStore.Container, "gitea": m.Gitea.Container}
	if m.Kubernetes != nil {
		services["kubernetes"] = m.Kubernetes.Container
	}
	for service, id := range services {
		raw, err := run(ctx, "inspect", id)
		if err != nil {
			return err
		}
		var containers []container.InspectResponse
		if json.Unmarshal(raw, &containers) != nil || len(containers) != 1 {
			return fmt.Errorf("%w: invalid Docker inspection", ErrLab)
		}
		c := containers[0]
		if c.State == nil || c.Config == nil || c.NetworkSettings == nil {
			return fmt.Errorf("%w: incomplete Docker inspection", ErrLab)
		}
		port := map[string]string{"postgres": "5432/tcp", "objectstore": "9000/tcp", "gitea": "3000/tcp", "kubernetes": "6443/tcp"}[service]
		address := map[string]string{"postgres": m.Postgres.DSN, "objectstore": m.ObjectStore.Endpoint, "gitea": m.Gitea.URL}[service]
		if service == "kubernetes" {
			address = m.Kubernetes.Endpoint
		}
		endpoint, err := url.Parse(address)
		if err != nil {
			return fmt.Errorf("%w: invalid service endpoint", ErrLab)
		}
		bindings := c.NetworkSettings.Ports[network.MustParsePort(port)]
		if len(bindings) != 1 || bindings[0].HostPort != endpoint.Port() || !bindings[0].HostIP.IsLoopback() || bindings[0].HostIP.String() != endpoint.Hostname() {
			return fmt.Errorf("%w: endpoint does not belong to the owned service", ErrLab)
		}
		if !c.State.Running || c.Config.Labels["io.infraweave.lab.owner"] != m.Owner || c.Config.Labels["io.infraweave.lab.batch"] != m.Batch ||
			c.Config.Labels["io.infraweave.lab.managed-by"] != "terraform" ||
			c.Config.Labels["io.infraweave.lab.service"] != service {
			return fmt.Errorf("%w: service ownership or readiness", ErrLab)
		}
	}
	return nil
}

// UseLab verifies a Terraform-owned batch before borrowing its dependencies.
// Close removes only this suite's databases; Terraform retains sole ownership
// of containers, volumes and the network. Each process gets a namespace so
// capture replays and independent test binaries cannot share fixture names.
func (d *Dependencies) UseLab(ctx context.Context, file string) error {
	return d.useLab(ctx, file, dockerLab)
}

func (d *Dependencies) useLab(ctx context.Context, file string, run labCommand) error {
	raw, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("%w: read manifest: %w", ErrLab, err)
	}
	m, err := readLab(raw)
	if err != nil {
		return err
	}
	if err := verifyLab(ctx, m, run); err != nil {
		return err
	}
	if m.Kubernetes != nil {
		cluster, err := labKubernetesClient(m)
		if err != nil {
			return err
		}
		if err := cluster.Ready(ctx); err != nil {
			return err
		}
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Errorf("%w: suite namespace: %w", ErrLab, err)
	}
	d.lab = m
	d.labNamespace = hex.EncodeToString(random[:])
	d.serial.Store(int64(binary.BigEndian.Uint64(random[:]) & ((1 << 62) - 1)))
	return nil
}

func (d *Dependencies) labGitea() (*Gitea, error) {
	ca, err := os.ReadFile(d.lab.Gitea.CAFile)
	if err != nil {
		return nil, fmt.Errorf("%w: read Gitea CA: %w", ErrLab, err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("%w: invalid Gitea CA", ErrLab)
	}
	return &Gitea{URL: d.lab.Gitea.URL, CAFile: d.lab.Gitea.CAFile, Client: &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, ForceAttemptHTTP2: true}, Timeout: 30 * time.Second,
	}}, nil
}

// WriteLabPKI creates the same FIPS-compatible fixture CA used by the harness.
// Terraform uploads the server pair; the private batch directory owns all files.
func WriteLabPKI(directory string) error {
	if err := os.Mkdir(directory, 0o700); err != nil {
		return fmt.Errorf("%w: create PKI directory: %w", ErrLab, err)
	}
	_, err := newGiteaPKI(directory)
	return err
}
