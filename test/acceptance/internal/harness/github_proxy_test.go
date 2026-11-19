package harness

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestUnitGitHubProxyPreservesTLSAndNativeRequests(t *testing.T) {
	t.Parallel()
	type observation struct{ host, path, method, signature, body string }
	var mu sync.Mutex
	var got []observation
	proxy, err := NewGitHubProxy(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		got = append(got, observation{r.Host, r.URL.RequestURI(), r.Method, r.Header.Get("X-Hub-Signature-256"), string(data)})
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(proxy.CA) {
		t.Fatal("missing verification root")
	}
	address, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: http.ProxyURL(address), TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	body := []byte(`{"requested_action":{"identifier":"preview"}}`)
	mac := hmac.New(sha256.New, bytes.Repeat([]byte{9}, 32))
	_, _ = mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	paths := []string{"https://api.github.com/repos/acme/infra/collaborators/developer/permission?native=1", "https://github.com/acme/infra.git/info/refs?service=git-upload-pack"}
	for _, target := range paths {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, target, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Hub-Signature-256", signature)
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != 204 {
			t.Fatal(response.StatusCode)
		}
	}
	mu.Lock()
	observed := append([]observation{}, got...)
	mu.Unlock()
	if len(observed) != 2 {
		t.Fatal("missing tunneled requests", observed)
	}
	for index, item := range observed {
		original, _ := url.Parse(paths[index])
		if item.host != original.Host || item.path != original.RequestURI() || item.method != http.MethodPost || item.signature != signature || item.body != string(body) {
			t.Fatal("proxy changed authenticated native bytes/identity", item)
		}
	}
	if !reflect.DeepEqual(proxy.Hosts(), []string{"api.github.com:443", "github.com:443"}) {
		t.Fatal("unexpected authority", proxy.Hosts())
	}
}

func TestUnitGitHubProxyRejectsUnownedDestinationsAndUntrustedTLS(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	calls := 0
	proxy, err := NewGitHubProxy(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { mu.Lock(); calls++; mu.Unlock(); w.WriteHeader(204) }))
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	address, _ := url.Parse(proxy.URL)
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(proxy.CA)
	for _, tc := range []struct {
		name, target, serverName string
		trusted                  bool
	}{
		{"foreign host", "https://unowned.invalid/", "", true},
		{"wrong port", "https://api.github.com:444/", "", true},
		{"untrusted CA", "https://api.github.com/", "", false},
		{"wrong TLS name", "https://api.github.com/", "unowned.invalid", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: tc.serverName}
			if tc.trusted {
				tlsConfig.RootCAs = roots
			}
			transport := &http.Transport{Proxy: http.ProxyURL(address), TLSClientConfig: tlsConfig}
			defer transport.CloseIdleConnections()
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, tc.target, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := (&http.Client{Transport: transport}).Do(request)
			if response != nil {
				_ = response.Body.Close()
			}
			if err == nil {
				t.Fatal("unowned/untrusted request succeeded")
			}
		})
	}
	mu.Lock()
	count := calls
	mu.Unlock()
	if count != 0 {
		t.Fatal("refusal reached fake handler", count)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, proxy.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Fatal("plain forward proxy accepted", response.StatusCode)
	}
	for _, host := range proxy.Hosts() {
		if host != "api.github.com:443" && host != "github.com:443" {
			t.Fatal("denied host reached owned listener", host)
		}
	}
}

func TestUnitScenarioEnvironmentCopiesWithoutGlobalMutation(t *testing.T) {
	t.Parallel()
	before := os.Getenv("HTTPS_PROXY")
	world := &World{}
	world.Setenv("HTTPS_PROXY", "http://127.0.0.1:7777")
	env := world.Environment()
	env[0] = "HTTPS_PROXY=http://unowned.invalid"
	if strings.Join(world.Environment(), ";") != "HTTPS_PROXY=http://127.0.0.1:7777" || os.Getenv("HTTPS_PROXY") != before {
		t.Fatal("verification proxy escaped its scenario")
	}
}
