package harness

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
)

// GitHubProxy is a verification-only CONNECT tunnel. It never resolves or
// forwards to the public network: both exact GitHub authorities terminate TLS
// at its owned fake, whose certificate authenticates their real DNS names.
// api.github.com serves the native API; github.com serves authenticated Git
// fetches performed by the launcher before it starts a runner container.
type GitHubProxy struct {
	URL           string
	CA            []byte
	target        string
	public, proxy *httptest.Server
	mu            sync.Mutex
	connections   map[net.Conn]struct{}
	hosts         []string
}

// NewGitHubProxy starts owned listeners and trusts no destinations supplied by
// clients. Original TLS SNI, Host, methods, paths, bodies and signatures pass
// through unchanged. Call Close after stopping the scenario's processes.
func NewGitHubProxy(handler http.Handler) (*GitHubProxy, error) {
	certificate, ca, err := githubCertificate()
	if err != nil {
		return nil, err
	}
	target := httptest.NewUnstartedServer(handler)
	target.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	target.StartTLS()
	p := &GitHubProxy{CA: ca, public: target, target: target.Listener.Addr().String(), connections: map[net.Conn]struct{}{}}
	p.proxy = httptest.NewServer(http.HandlerFunc(p.connect))
	p.URL = p.proxy.URL
	return p, nil
}

// Hosts returns only the allowlisted authorities accepted by the tunnel.
func (p *GitHubProxy) Hosts() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string{}, p.hosts...)
}

// Close ends every owned connection and listener, including idle keep-alives.
func (p *GitHubProxy) Close() {
	p.proxy.CloseClientConnections()
	p.mu.Lock()
	for conn := range p.connections {
		_ = conn.Close()
	}
	p.mu.Unlock()
	p.proxy.Close()
	p.public.CloseClientConnections()
	p.public.Close()
}

func (p *GitHubProxy) connect(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(r.Host)
	if r.Method != http.MethodConnect {
		http.Error(w, "CONNECT required", http.StatusMethodNotAllowed)
		return
	}
	if host != "api.github.com:443" && host != "github.com:443" {
		http.Error(w, "CONNECT target refused", http.StatusForbidden)
		return
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	upstream, err := dialer.DialContext(r.Context(), "tcp", p.target)
	if err != nil {
		http.Error(w, "verification destination unavailable", http.StatusBadGateway)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(w, "CONNECT unavailable", http.StatusInternalServerError)
		return
	}
	client, buffer, err := hijacker.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	p.mu.Lock()
	p.connections[client] = struct{}{}
	p.connections[upstream] = struct{}{}
	p.hosts = append(p.hosts, host)
	p.mu.Unlock()
	defer func() {
		_ = client.Close()
		_ = upstream.Close()
		p.mu.Lock()
		delete(p.connections, client)
		delete(p.connections, upstream)
		p.mu.Unlock()
	}()
	if _, err := buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err := buffer.Flush(); err != nil {
		return
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, buffer); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, upstream); done <- struct{}{} }()
	select {
	case <-done:
	case <-r.Context().Done():
	}
	_ = client.Close()
	_ = upstream.Close()
	<-done
}

func githubCertificate() (tls.Certificate, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	now := time.Now()
	template := x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "infraweave GitHub verification"},
		DNSNames: []string{"api.github.com", "github.com"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	certificate, err := tls.X509KeyPair(ca, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}))
	return certificate, ca, err
}
