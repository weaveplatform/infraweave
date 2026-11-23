package harness

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
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
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/weaveplatform/infraweave/test/deps"
)

// ErrPodNetwork is a lab cluster whose run Pods have no route to the host
// the suite finds.
var ErrPodNetwork = errors.New("no address at which the lab cluster's Pods reach the host")

// PodNetwork is where the lab cluster's run Pods reach a listener of the
// host. On Linux the Docker daemon is the host's, and the lab network's
// gateway is the host's own address, which host processes dial too.
// Elsewhere the daemon runs in a virtual machine, which forwards
// host.docker.internal's connections to the host's loopback, an address
// no host process can dial; a forward proxy on loopback takes theirs
// there instead.
type PodNetwork struct {
	// Host is the IP a run Pod dials for the host.
	Host string
	// Listen is the IP a listener for run Pods binds.
	Listen string
	// Proxy is the URL of the forward proxy through which a host process
	// reaches Host's listeners, or "" where it dials Host itself.
	Proxy string
	srv   *http.Server
}

// StartPodNetwork finds where the run Pods of cluster reach the host and,
// where host processes cannot dial that address, starts the proxy that
// takes them to it.
func StartPodNetwork(ctx context.Context, cluster *deps.Kubernetes) (*PodNetwork, error) {
	if runtime.GOOS == "linux" {
		out, err := dockerCLI(ctx, nil, "network", "inspect", "--format",
			"{{range .IPAM.Config}}{{.Gateway}} {{end}}", cluster.Network)
		if err != nil {
			return nil, err
		}
		for _, gw := range strings.Fields(string(out)) {
			if ip := net.ParseIP(gw); ip != nil && ip.To4() != nil {
				return &PodNetwork{Host: gw, Listen: gw}, nil
			}
		}
		return nil, fmt.Errorf("%w: the network %s has no IPv4 gateway", ErrPodNetwork, cluster.Network)
	}
	out, err := dockerCLI(ctx, nil, "exec", cluster.Container, "nslookup", "host.docker.internal")
	if err != nil {
		return nil, err
	}
	host := ""
	named := false
	for line := range strings.Lines(string(out)) {
		named = named || strings.HasPrefix(line, "Name:")
		if a, ok := strings.CutPrefix(strings.TrimSpace(line), "Address:"); ok && named {
			if ip := net.ParseIP(strings.TrimSpace(a)); ip != nil && ip.To4() != nil {
				host = ip.String()
				break
			}
		}
	}
	if host == "" {
		return nil, fmt.Errorf("%w: host.docker.internal resolves to %q", ErrPodNetwork, out)
	}
	n := &PodNetwork{Host: host, Listen: "127.0.0.1"}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("the pod network's proxy: %w", err)
	}
	n.Proxy = "http://" + ln.Addr().String()
	n.srv = &http.Server{Handler: http.HandlerFunc(n.connect), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = n.srv.Serve(ln) }()
	return n, nil
}

// connect tunnels a CONNECT to a port of Host to that port of the host's
// loopback, and refuses every other request.
func (n *PodNetwork) connect(w http.ResponseWriter, r *http.Request) {
	host, port, err := net.SplitHostPort(r.Host)
	if r.Method != http.MethodConnect || err != nil || host != n.Host {
		http.Error(w, "the pod network's proxy tunnels only to "+n.Host, http.StatusForbidden)
		return
	}
	out, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(r.Context(), "tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	in, buf, err := http.NewResponseController(w).Hijack()
	if err != nil {
		_ = out.Close()
		return
	}
	go splice(in, buf, out)
}

// splice copies between a hijacked client connection, whose buffered
// reads come first, and out, until either side ends.
func splice(in net.Conn, buf *bufio.ReadWriter, out net.Conn) {
	defer in.Close()
	defer out.Close()
	if _, err := buf.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if buf.Flush() != nil {
		return
	}
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(out, buf)
		close(done)
	}()
	_, _ = io.Copy(in, out)
	<-done
}

// Transport is an HTTP transport that reaches Host's listeners from this
// process, trusting roots.
func (n *PodNetwork) Transport(roots []byte) (*http.Transport, error) {
	t := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // the standard library's
	t.Proxy = nil
	if n.Proxy != "" {
		u, err := url.Parse(n.Proxy)
		if err != nil {
			return nil, fmt.Errorf("the pod network's proxy: %w", err)
		}
		t.Proxy = http.ProxyURL(u)
	}
	pool, err := certPool(roots)
	if err != nil {
		return nil, err
	}
	t.TLSClientConfig.RootCAs = pool
	return t, nil
}

// Environment is what a host process needs in its environment to reach
// Host's listeners: the proxy, where there is one.
func (n *PodNetwork) Environment() []string {
	if n.Proxy == "" {
		return nil
	}
	return []string{"HTTPS_PROXY=" + n.Proxy, "HTTP_PROXY=" + n.Proxy}
}

// Close stops the proxy.
func (n *PodNetwork) Close(ctx context.Context) error {
	if n.srv == nil {
		return nil
	}
	if err := n.srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("the pod network's proxy: %w", err)
	}
	return nil
}

// dockerCLI runs the Docker CLI with args against the suite's daemon,
// writing stdin, when not nil, to its standard input.
func dockerCLI(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = stdin
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker %s: %w: %s", args[0], err, stderr.String())
	}
	return out, nil
}

// BootstrapRepository is the repository of the bootstrap image the suite
// imports into the lab cluster's node.
const BootstrapRepository = "infraweave.test/bootstrap"

// BootstrapImage is the digest reference of a run Pod's bootstrap image
// holding the current infraweave's Linux build at
// /usr/local/bin/infraweave, which the suite builds as an OCI archive and
// imports into cluster's node once, so the kubelet finds it present and
// pulls nothing.
func (l *Launchers) BootstrapImage(ctx context.Context, cluster *deps.Kubernetes) (string, error) {
	l.bootstrapMu.Lock()
	defer l.bootstrapMu.Unlock()
	if ref, ok := l.bootstrap[cluster.Container]; ok {
		return ref, nil
	}
	archive, manifest, err := ociArchive(l.Runner, l.Arch)
	if err != nil {
		return "", err
	}
	if _, err := dockerCLI(ctx, bytes.NewReader(archive), "exec", "-i", cluster.Container,
		"ctr", "--namespace", "k8s.io", "images", "import", "-"); err != nil {
		return "", err
	}
	ref := BootstrapRepository + "@" + manifest
	if l.bootstrap == nil {
		l.bootstrap = map[string]string{}
	}
	l.bootstrap[cluster.Container] = ref
	return ref, nil
}

// NodeImage pulls the released runner image into cluster's node once,
// through the node's own container runtime, so the kubelet finds it
// present when a run Pod or an initialization command's helper Pod starts
// from it. The kubelet pulls one image at a time, so on a node that
// concurrent scenarios share, a Pod's first pull otherwise queues behind
// every other scenario's and spends a deadline its scenario sets for the
// Pod's own work, such as a worker initialization policy's. A failed pull
// is retried, as the kubelet would retry it, and only a pulled image is
// remembered.
func (l *Launchers) NodeImage(ctx context.Context, cluster *deps.Kubernetes, image string) error {
	if !runnerReferences[image] {
		return fmt.Errorf("%w: %q", ErrRunnerImage, image)
	}
	l.nodeMu.Lock()
	defer l.nodeMu.Unlock()
	key := cluster.Container + " " + image
	if l.node[key] {
		return nil
	}
	var err error
	for range nodePulls {
		if _, err = dockerCLI(ctx, nil, "exec", cluster.Container, "crictl", "pull", image); err == nil || ctx.Err() != nil {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("pull runner image %s into the node: %w", image, err)
	}
	if l.node == nil {
		l.node = map[string]bool{}
	}
	l.node[key] = true
	return nil
}

// nodePulls is how many times NodeImage tries a pull.
const nodePulls = 3

// ociArchive is an OCI image layout, as a tar archive, of one layer that
// holds binary at /usr/local/bin/infraweave for linux/arch, and its
// manifest's digest.
func ociArchive(binary, arch string) ([]byte, string, error) {
	bin, err := os.ReadFile(binary)
	if err != nil {
		return nil, "", fmt.Errorf("the bootstrap image: %w", err)
	}
	var layer bytes.Buffer
	lt := tar.NewWriter(&layer)
	epoch := time.Unix(0, 0)
	for _, dir := range []string{"usr/", "usr/local/", "usr/local/bin/"} {
		if err := lt.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: dir, Mode: 0o755, ModTime: epoch}); err != nil {
			return nil, "", fmt.Errorf("the bootstrap image: %w", err)
		}
	}
	if err := lt.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: "usr/local/bin/infraweave", Mode: 0o755, Size: int64(len(bin)), ModTime: epoch,
	}); err != nil {
		return nil, "", fmt.Errorf("the bootstrap image: %w", err)
	}
	if _, err := lt.Write(bin); err != nil {
		return nil, "", fmt.Errorf("the bootstrap image: %w", err)
	}
	if err := lt.Close(); err != nil {
		return nil, "", fmt.Errorf("the bootstrap image: %w", err)
	}
	layerDigest := sha(layer.Bytes())
	config, err := json.Marshal(map[string]any{
		"architecture": arch, "os": "linux",
		"config": map[string]any{"User": "1983:1983"},
		"rootfs": map[string]any{"type": "layers", "diff_ids": []string{layerDigest}},
	})
	if err != nil {
		return nil, "", fmt.Errorf("the bootstrap image: %w", err)
	}
	manifest, err := json.Marshal(map[string]any{
		"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json",
		"config": descriptor("application/vnd.oci.image.config.v1+json", config),
		"layers": []any{descriptor("application/vnd.oci.image.layer.v1.tar", layer.Bytes())},
	})
	if err != nil {
		return nil, "", fmt.Errorf("the bootstrap image: %w", err)
	}
	entry := descriptor("application/vnd.oci.image.manifest.v1+json", manifest)
	entry["platform"] = map[string]string{"architecture": arch, "os": "linux"}
	// The image is named by its digest reference, which the kubelet
	// looks up; containerd answers no other name's digest for it.
	entry["annotations"] = map[string]string{"io.containerd.image.name": BootstrapRepository + "@" + sha(manifest)}
	index, err := json.Marshal(map[string]any{
		"schemaVersion": 2, "mediaType": "application/vnd.oci.image.index.v1+json",
		"manifests": []any{entry},
	})
	if err != nil {
		return nil, "", fmt.Errorf("the bootstrap image: %w", err)
	}
	var out bytes.Buffer
	ot := tar.NewWriter(&out)
	files := []struct {
		name string
		body []byte
	}{
		{"oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`)},
		{"index.json", index},
		{blob(config), config},
		{blob(manifest), manifest},
		{blob(layer.Bytes()), layer.Bytes()},
	}
	for _, f := range files {
		if err := ot.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: f.name, Mode: 0o644, Size: int64(len(f.body)), ModTime: epoch,
		}); err != nil {
			return nil, "", fmt.Errorf("the bootstrap image: %w", err)
		}
		if _, err := ot.Write(f.body); err != nil {
			return nil, "", fmt.Errorf("the bootstrap image: %w", err)
		}
	}
	if err := ot.Close(); err != nil {
		return nil, "", fmt.Errorf("the bootstrap image: %w", err)
	}
	return out.Bytes(), sha(manifest), nil
}

func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func blob(b []byte) string { return "blobs/sha256/" + strings.TrimPrefix(sha(b), "sha256:") }

func descriptor(mediaType string, b []byte) map[string]any {
	return map[string]any{"mediaType": mediaType, "digest": sha(b), "size": len(b)}
}

// bootstrapState is the bootstrap images Launchers imported, by the
// container of the node that holds each, and the runner images it pulled
// into nodes, by node container and image. Pulls take their own lock, so
// an import never waits behind a pull.
type bootstrapState struct {
	bootstrapMu sync.Mutex
	bootstrap   map[string]string
	nodeMu      sync.Mutex
	node        map[string]bool
}

// ErrRoots is a trust bundle that holds no PEM certificate.
var ErrRoots = errors.New("the roots hold no PEM certificate")

// certPool is the system's roots with pem's.
func certPool(pem []byte) (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("the system's roots: %w", err)
	}
	if len(pem) > 0 && !pool.AppendCertsFromPEM(pem) {
		return nil, ErrRoots
	}
	return pool, nil
}
