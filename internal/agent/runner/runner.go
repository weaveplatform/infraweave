package runner

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/agent/exec"
	"github.com/weaveplatform/infraweave/internal/agent/handoff"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// Run reads the launcher's handoff from in and executes the run it hands
// over in the working directory, the container's workspace, reporting
// and uploading its log through the client doer makes, which trusts
// roots: the system's with the handoff's Roots, or nil for the system's
// alone. The phases' masked output goes to
// standard output, which the launcher reads, too. stop carries the
// launcher's SIGINTs, each a stop of the run.
//
// ref: decision 0008 § "The launcher and the runner"
func Run(ctx context.Context, in io.Reader, doer func(roots *x509.CertPool) wire.HttpRequestDoer, log *slog.Logger, stop <-chan os.Signal) error {
	h, err := handoff.Read(in)
	if err != nil {
		return err //nolint:wrapcheck // handoff names the read
	}
	roots, err := trust(h.Roots)
	if err != nil {
		return err
	}
	c := client.New(h.Endpoint, doer(roots))
	return exec.Run(ctx, h, exec.Deps{ //nolint:wrapcheck // exec names the step
		Reports:   c,
		Logs:      c,
		Uploads:   c,
		Log:       log,
		Out:       os.Stdout,
		Workspace: ".",
		Environ:   os.Environ(),
		Stop:      stop,
	})
}

// HandBack opens the working directory, a run's workspace, to its
// launcher's user, as Run does as the run ends. The launcher runs it in a
// container of its own over a run container that ended before its
// runner did, as a killed one.
func HandBack() error { return exec.HandBack(".") } //nolint:wrapcheck // names the path

// ErrRoots is a handoff whose Roots hold no PEM certificate.
var ErrRoots = errors.New("runner: the handoff's roots hold no PEM certificate")

// trust is the system's roots with pem's, or nil when pem is empty.
func trust(pem []byte) (*x509.CertPool, error) {
	if len(pem) == 0 {
		return nil, nil //nolint:nilnil // nil is the system's roots
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("runner: the system's roots: %w", err)
	}
	if !roots.AppendCertsFromPEM(pem) {
		return nil, ErrRoots
	}
	return roots, nil
}

// Client makes the runner's HTTP client, which bounds each call by
// timeout and trusts roots, or the system's when nil.
func Client(timeout time.Duration) func(*x509.CertPool) wire.HttpRequestDoer {
	return func(roots *x509.CertPool) wire.HttpRequestDoer {
		t := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // the standard library's
		t.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
		return &http.Client{Timeout: timeout, Transport: t}
	}
}
