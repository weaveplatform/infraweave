package sourcetest

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/vcslab"
)

// LabEnv returns the lab the vcs-lab job names, skipping t unless every
// one of need, which are vcslab's Env names, is set: only that job sets
// them, and it fails a provider whose variables are missing before any
// test runs.
func LabEnv(t *testing.T, need ...string) vcslab.Lab {
	t.Helper()
	lab, err := vcslab.FromEnv(need...)
	if err != nil {
		t.Skipf("%v: the vcs-lab job runs this test", err)
	}
	return lab
}

// LabRoots returns the certificates in the PEM file caFile, the lab
// container's own, and a client that trusts them alone, failing t when
// the file holds none.
func LabRoots(t *testing.T, caFile string) (*x509.CertPool, *http.Client) {
	t.Helper()
	roots, err := labRoots(caFile)
	if err != nil {
		t.Fatal(err)
	}
	return roots, &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
	}}
}

// ErrNoCertificate reports a CA file that holds no PEM certificate.
var ErrNoCertificate = errors.New("sourcetest: no certificate")

func labRoots(caFile string) (*x509.CertPool, error) {
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("sourcetest: lab CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("%w in %s", ErrNoCertificate, caFile)
	}
	return roots, nil
}

// Lab is what a Source must find in repo, a repository seed.sh pushed
// its history to, whose host lacks missing.
func Lab(repo, missing port.Repository) Repository {
	return Repository{
		Repo: repo, Missing: missing,
		Branch: vcslab.Branch, Tag: vcslab.Tag, Tagged: vcslab.Tagged,
		Base: vcslab.Base, Head: vcslab.Head,
		File: vcslab.File, Content: []byte(vcslab.Content), Changed: vcslab.Changed(),
	}
}

// ErrHost reports a host that answers a fetch with a server error.
var ErrHost = errors.New("sourcetest: the host failed the fetch")

// advertisement is the content type of a smart-HTTP ref advertisement
// for a fetch.
const advertisement = "application/x-git-upload-pack-advertisement"

// Reads reports whether git, signing in as c's username and token, may
// fetch the repository at cloneURL: whether its smart-HTTP ref
// advertisement answers, the first request of every clone. Any other
// answer below 500 is a refusal, as hosts refuse a repository by 401,
// 403, 404 or a sign-in page; a server error or no answer is an error.
func Reads(
	ctx context.Context,
	client *http.Client,
	cloneURL string,
	c port.Credential,
) (bool, error) {
	u := strings.TrimSuffix(cloneURL, "/") + "/info/refs?service=git-upload-pack"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, fmt.Errorf("sourcetest: %w", err)
	}
	req.SetBasicAuth(c.Username, string(c.Token))
	req.Header.Set("Git-Protocol", "version=2")
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("sourcetest: fetch %s: %w", cloneURL, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= http.StatusInternalServerError {
		return false, fmt.Errorf("%w: %s: %s", ErrHost, cloneURL, resp.Status)
	}
	return resp.StatusCode == http.StatusOK &&
		resp.Header.Get("Content-Type") == advertisement, nil
}

// Scope fails t unless c reads the repository at own, and reads the one
// at other exactly when c is wide: a run's minted token reaches its own
// repository alone, and the token an integration lends reaches every
// repository the integration does.
func Scope(t *testing.T, client *http.Client, c port.Credential, own, other string, wide bool) {
	t.Helper()
	for _, r := range []struct {
		url  string
		want bool
	}{{own, true}, {other, wide}} {
		got, err := Reads(t.Context(), client, r.url, c)
		if err != nil || got != r.want {
			t.Errorf("the credential reads %s = %t, %v; want %t", r.url, got, err, r.want)
		}
	}
}
