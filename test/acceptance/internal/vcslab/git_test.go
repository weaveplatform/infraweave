package vcslab

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/pktline"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
)

func TestUnitNativeGitHTTPFailuresExposeOnlySafeStatus(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 500} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			const token = "private-native-git-credential-32bytes"
			const body = "private-native-provider-response"
			calls := 0
			r := chi.NewRouter()
			r.Get("/*", func(w http.ResponseWriter, req *http.Request) {
				calls++
				user, password, ok := req.BasicAuth()
				if !ok || user != "pat" || password != token || req.URL.Path != "/private-project/_git/private-repository/info/refs" ||
					req.URL.Query().Get("service") != "git-upload-pack" {
					t.Error("Git discovery did not preserve its native path, credentials or service")
				}
				w.Header().Set("X-Private-Provider", "private-provider-header")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(body + " " + token))
			})
			srv := httptest.NewTLSServer(r)
			defer srv.Close()
			ca := filepath.Join(t.TempDir(), "ca.pem")
			if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
				t.Fatal(err)
			}
			parent := t.TempDir()
			n := &Native{Provider: "azure-devops", Host: srv.URL, Namespace: "private-project", Repository: "private-repository"}
			head, err := n.Push(t.Context(), parent, "main", "proof", GitCredentials{User: "pat", Token: token, CAFile: ca})
			if calls != 1 || head != "" || !errors.Is(err, ErrNative) {
				t.Fatal("native Git failure did not remain a failed clone")
			}
			category := "http"
			switch status {
			case 401:
				category = "authentication"
			case 403:
				category = "authorization"
			case 404:
				category = "not_found"
			}
			if !strings.Contains(err.Error(), "operation=clone category="+category+" status="+strconv.Itoa(status)) {
				t.Fatal("native Git failure lost its safe operation/status category")
			}
			for _, rendered := range []string{fmt.Sprint(err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
				for _, private := range []string{token, body, "private-provider-header", "private-project", "private-repository", srv.URL} {
					if strings.Contains(rendered, private) {
						t.Fatal("native Git diagnostic disclosed request or response material")
					}
				}
			}
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) != 0 {
				t.Fatal("failed clone left its owned workspace behind")
			}
		})
	}
}

func TestUnitNativeGitTLSCancellationAndReferenceFailures(t *testing.T) {
	for _, name := range []string{"tls", "canceled", "timeout", "reference", "protocol"} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			r := chi.NewRouter()
			r.Get("/*", func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
				_, _ = io.WriteString(w, "001e# service=git-upload-pack\n0000")
				if name == "protocol" {
					_, _ = io.WriteString(w, "000xprivate-invalid-advertisement")
					return
				}
				refs := packp.NewAdvRefs()
				// An empty native advertisement is a reference failure, not an
				// authentication failure or a response-body diagnostic.
				if err := refs.Encode(w); err != nil {
					t.Error("could not encode native refs")
				}
			})
			srv := httptest.NewUnstartedServer(r)
			srv.Config.ErrorLog = log.New(io.Discard, "", 0)
			srv.StartTLS()
			defer srv.Close()
			credential := GitCredentials{User: "pat", Token: "private-git-test-token-with-32-bytes"}
			if name != "tls" {
				credential.CAFile = filepath.Join(t.TempDir(), "ca.pem")
				if err := os.WriteFile(credential.CAFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx := t.Context()
			if name == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if name == "timeout" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Unix(0, 0))
				defer cancel()
			}
			parent := t.TempDir()
			n := &Native{Provider: "azure-devops", Host: srv.URL, Namespace: "private-project", Repository: "private-repository"}
			head, err := n.Push(ctx, parent, "main", "proof", credential)
			if head != "" || !errors.Is(err, ErrNative) || !strings.Contains(err.Error(), "operation=clone category="+name+" status=0") {
				t.Fatal("native Git failure lost its fixed category", err)
			}
			wantCalls := 0
			if name == "reference" || name == "protocol" {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatal("native discovery bypassed TLS/cancellation or unexpected retries")
			}
			for _, rendered := range []string{fmt.Sprint(err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
				for _, private := range []string{credential.Token, "private", srv.URL} {
					if strings.Contains(rendered, private) {
						t.Fatal("native Git diagnostic leaked lower-level values")
					}
				}
			}
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) != 0 {
				t.Fatal("failed clone retained an owned workspace")
			}
		})
	}
}

type privateGitError struct{ next error }

func (e *privateGitError) Error() string { panic("diagnostics must not format a native error") }
func (e *privateGitError) Unwrap() error { return e.next }

func TestUnitNativeGitDiagnosticsDiscardCausesAndBoundTraversal(t *testing.T) {
	cycle := &privateGitError{}
	cycle.next = cycle
	for _, tc := range []struct {
		name  string
		cause error
		want  string
	}{
		{"unknown", &privateGitError{}, "other"},
		{"cyclic wrapper", cycle, "other"},
		{"network timeout", &net.DNSError{Err: "private-network-error", Name: "private-host", IsTimeout: true}, "timeout"},
		{"network failure", &net.OpError{Op: "private-operation", Err: errors.New("private-network-error")}, "transport"},
		{"certificate", x509.HostnameError{Host: "private-host"}, "tls"},
		{"server protocol", &pktline.ErrorLine{Text: "private-server-error"}, "protocol"},
		{"wrapped branch", &plumbing.PermanentError{Err: fmt.Errorf("private-wrapper: %w", git.ErrBranchNotFound)}, "reference"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := nativeGitFailure("push", tc.cause)
			if !errors.Is(err, ErrNative) || err.Error() != ErrNative.Error()+": Git operation=push category="+tc.want+" status=0" {
				t.Fatal("native Git diagnostic lost its closed-set category")
			}
			for _, rendered := range []string{fmt.Sprint(err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
				if strings.Contains(rendered, "private") {
					t.Fatal("native Git diagnostic retained its secret-bearing cause")
				}
			}
		})
	}
}
