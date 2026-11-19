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
	"net/url"
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
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

func TestUnitNativeGitHTTPFailuresExposeOnlySafeStatus(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 429, 500} {
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
			phase := "advertise"
			if status == 401 || status == 403 || status == 404 {
				phase = "other" // go-git's status sentinels discard the response request.
			}
			if !strings.HasSuffix(err.Error(), " phase="+phase) {
				t.Fatal("native Git failure lost its safe HTTP phase")
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
	var deep error = plumbing.NewUnexpectedError(&githttp.Err{Response: &http.Response{StatusCode: 400}, Reason: "private-native-response"})
	for range 17 {
		deep = &privateGitError{next: deep}
	}
	for _, tc := range []struct {
		name  string
		cause error
		want  string
	}{
		{"unknown", &privateGitError{}, "other"},
		{"cyclic wrapper", cycle, "other"},
		{"excessive wrappers", deep, "other"},
		{"nil response", plumbing.NewUnexpectedError(&githttp.Err{Reason: "private-native-response"}), "other"},
		{"invalid status", plumbing.NewUnexpectedError(&githttp.Err{Response: &http.Response{StatusCode: 700}, Reason: "private-native-response"}), "other"},
		{"network timeout", &net.DNSError{Err: "private-network-error", Name: "private-host", IsTimeout: true}, "timeout"},
		{"network failure", &net.OpError{Op: "private-operation", Err: errors.New("private-network-error")}, "transport"},
		{"certificate", x509.HostnameError{Host: "private-host"}, "tls"},
		{"server protocol", &pktline.ErrorLine{Text: "private-server-error"}, "protocol"},
		{"wrapped branch", &plumbing.PermanentError{Err: fmt.Errorf("private-wrapper: %w", git.ErrBranchNotFound)}, "reference"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := nativeGitFailure("push", tc.cause)
			if !errors.Is(err, ErrNative) || err.Error() != ErrNative.Error()+": Git operation=push category="+tc.want+" status=0 phase=other" {
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

func TestUnitNativeGitPackFailuresRetainOnlySafeHTTPPhase(t *testing.T) {
	for _, phase := range []string{"upload-pack", "receive-pack"} {
		t.Run(phase, func(t *testing.T) {
			const secret = "private-native-protocol-credential-32bytes"
			calls := 0
			r := chi.NewRouter()
			r.Post("/*", func(w http.ResponseWriter, req *http.Request) {
				calls++
				user, password, ok := req.BasicAuth()
				if !ok || user != "pat" || password != secret || req.URL.Path != "/private-project/_git/private-repository/git-"+phase {
					t.Error("native pack request changed its credentials or protocol route")
				}
				_, _ = io.Copy(io.Discard, req.Body)
				w.Header().Set("X-Private-Provider", secret)
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, secret+" private-provider-response")
			})
			srv := httptest.NewTLSServer(r)
			defer srv.Close()
			endpoint, err := transport.NewEndpoint(srv.URL + "/private-project/_git/private-repository")
			if err != nil {
				t.Fatal(err)
			}
			client := githttp.NewClient(srv.Client())
			auth := &githttp.BasicAuth{Username: "pat", Password: secret}
			operation := "clone"
			var cause error
			if phase == "upload-pack" {
				session, err := client.NewUploadPackSession(endpoint, auth)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = session.Close() }()
				req := packp.NewUploadPackRequest()
				req.Wants = []plumbing.Hash{plumbing.NewHash(strings.Repeat("a", 40))}
				_, cause = session.UploadPack(t.Context(), req)
			} else {
				operation = "push"
				session, err := client.NewReceivePackSession(endpoint, auth)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = session.Close() }()
				req := packp.NewReferenceUpdateRequest()
				req.Commands = []*packp.Command{{Name: plumbing.NewBranchReferenceName("main"), New: plumbing.NewHash(strings.Repeat("a", 40))}}
				_, cause = session.ReceivePack(t.Context(), req)
			}
			if calls != 1 || cause == nil {
				t.Fatal("the genuine TLS pack exchange did not fail exactly once")
			}
			var native *githttp.Err
			var wrapper *plumbing.UnexpectedError
			if !errors.As(cause, &wrapper) {
				t.Fatal("the transport did not retain its typed HTTP failure")
			}
			if !errors.As(wrapper.Err, &native) || native.Response.StatusCode != 400 {
				t.Fatal("the transport did not return native HTTP 400")
			}
			err = nativeGitFailure(operation, cause)
			if err.Error() != ErrNative.Error()+": Git operation="+operation+" category=http status=400 phase="+phase || !errors.Is(err, ErrNative) {
				t.Fatal("the diagnostic did not retain its safe native phase/status")
			}
			if errors.As(err, &native) || errors.As(err, &wrapper) {
				t.Fatal("the public diagnostic retained a secret-bearing native error")
			}
			for _, rendered := range []string{fmt.Sprint(err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
				for _, private := range []string{secret, "private", srv.URL} {
					if strings.Contains(rendered, private) {
						t.Fatal("the native protocol diagnostic disclosed request or response material")
					}
				}
			}
		})
	}
}

func TestUnitNativeGitHTTPPhaseRejectsUntrustedMetadata(t *testing.T) {
	request := func(method, path, query string) *http.Request {
		return &http.Request{
			Method: method,
			URL:    &url.URL{Scheme: "https", Host: "private-host", Path: path, RawQuery: query, User: url.UserPassword("private-user", "private-token")},
		}
	}
	for _, tc := range []struct {
		name    string
		request *http.Request
	}{
		{"nil request", nil},
		{"nil URL", &http.Request{Method: http.MethodGet}},
		{"opaque URL", &http.Request{Method: http.MethodPost, URL: &url.URL{Opaque: "private-opaque", Path: "/git-upload-pack"}}},
		{"forced empty query", &http.Request{Method: http.MethodPost, URL: &url.URL{Path: "/git-upload-pack", ForceQuery: true}}},
		{"unknown path", request(http.MethodPost, "/private-path", "")},
		{"query path spoof", request(http.MethodPost, "/private-path", "path=/git-upload-pack")},
		{"wrong method", request(http.MethodDelete, "/git-upload-pack", "")},
		{"encoded suffix", request(http.MethodPost, "/private/%67it-upload-pack", "")},
		{"post with query", request(http.MethodPost, "/git-upload-pack", "private=token")},
		{"unknown service", request(http.MethodGet, "/info/refs", "service=private-service")},
		{"missing service", request(http.MethodGet, "/info/refs", "")},
		{"malformed query", request(http.MethodGet, "/info/refs", "service=git-upload-pack&private=%zz")},
		{"extra query", request(http.MethodGet, "/info/refs", "service=git-upload-pack&private=token")},
		{"repeated service", request(http.MethodGet, "/info/refs", "service=git-upload-pack&service=git-receive-pack")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cause := plumbing.NewUnexpectedError(&githttp.Err{Response: &http.Response{StatusCode: 400, Request: tc.request}, Reason: "private-response"})
			err := nativeGitFailure("clone", &privateGitError{next: cause})
			if err.Error() != ErrNative.Error()+": Git operation=clone category=http status=400 phase=other" {
				t.Fatal("untrusted metadata escaped the closed-set HTTP phase")
			}
		})
	}
}
