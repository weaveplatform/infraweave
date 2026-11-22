package deps

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type labKubernetes struct {
	Container  string `json:"container"`
	Endpoint   string `json:"endpoint"`
	Namespace  string `json:"namespace"`
	Kubeconfig string `json:"kubeconfig"`
}

func labKubernetesNamespace(owner string) string {
	sum := sha256.Sum256([]byte(owner))
	return "iw-" + hex.EncodeToString(sum[:16])
}

// Kubernetes is a borrowed Terraform-owned local cluster. No method provisions
// infrastructure or substitutes a cluster when its connection fails.
type Kubernetes struct {
	Endpoint   string
	Namespace  string
	Kubeconfig string
	owner      string
	client     *http.Client
}

// Kubernetes borrows the explicitly provisioned cluster or fails; ordinary
// acceptance never creates an untracked replacement for a missing lab.
func (d *Dependencies) Kubernetes(ctx context.Context) (*Kubernetes, error) {
	if d.lab == nil || d.lab.Kubernetes == nil {
		return nil, fmt.Errorf("%w: this batch has no Kubernetes cluster", ErrLab)
	}
	cluster, err := labKubernetesClient(d.lab)
	if err != nil {
		return nil, err
	}
	if err := cluster.Ready(ctx); err != nil {
		return nil, err
	}
	return cluster, nil
}

func labKubernetesClient(m *labManifest) (*Kubernetes, error) {
	f, err := os.Open(m.Kubernetes.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("%w: read private Kubernetes configuration", ErrLab)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return nil, fmt.Errorf("%w: Kubernetes configuration exceeds its bound", ErrLab)
	}
	defer clear(data)
	var config struct {
		Clusters []struct {
			Name    string `json:"name"`
			Cluster struct {
				Server   string `json:"server"`
				CA       string `json:"certificate-authority-data"` //nolint:tagliatelle // Kubernetes kubeconfig field
				Insecure bool   `json:"insecure-skip-tls-verify"`   //nolint:tagliatelle // Kubernetes kubeconfig field
			} `json:"cluster"`
		} `json:"clusters"`
		Users []struct {
			Name string `json:"name"`
			User struct {
				Certificate  string          `json:"client-certificate-data"` //nolint:tagliatelle // Kubernetes kubeconfig field
				Key          string          `json:"client-key-data"`         //nolint:tagliatelle // Kubernetes kubeconfig field
				Exec         json.RawMessage `json:"exec"`
				AuthProvider json.RawMessage `json:"auth-provider"` //nolint:tagliatelle // Kubernetes kubeconfig field
				Token        string          `json:"token"`
			} `json:"user"`
		} `json:"users"`
		Contexts []struct {
			Name    string `json:"name"`
			Context struct {
				Cluster   string `json:"cluster"`
				User      string `json:"user"`
				Namespace string `json:"namespace"`
			} `json:"context"`
		} `json:"contexts"`
		CurrentContext string `json:"current-context"` //nolint:tagliatelle // Kubernetes kubeconfig field
	}
	if json.Unmarshal(data, &config) != nil || len(config.Clusters) != 1 || len(config.Users) != 1 || len(config.Contexts) != 1 {
		return nil, fmt.Errorf("%w: invalid Kubernetes configuration", ErrLab)
	}
	c, u, selected := config.Clusters[0], config.Users[0], config.Contexts[0]
	if c.Cluster.Server != m.Kubernetes.Endpoint || c.Cluster.Insecure || selected.Name != config.CurrentContext ||
		selected.Context.Cluster != c.Name || selected.Context.User != u.Name || selected.Context.Namespace != m.Kubernetes.Namespace ||
		len(u.User.Exec) != 0 || len(u.User.AuthProvider) != 0 || u.User.Token != "" {
		return nil, fmt.Errorf("%w: Kubernetes configuration identity or authentication", ErrLab)
	}
	ca, caErr := base64.StdEncoding.DecodeString(c.Cluster.CA)
	cert, certErr := base64.StdEncoding.DecodeString(u.User.Certificate)
	key, keyErr := base64.StdEncoding.DecodeString(u.User.Key)
	defer clear(key)
	roots := x509.NewCertPool()
	if caErr != nil || certErr != nil || keyErr != nil || !roots.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("%w: Kubernetes certificate encoding", ErrLab)
	}
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return nil, fmt.Errorf("%w: Kubernetes client certificate", ErrLab)
	}
	return &Kubernetes{
		Endpoint: m.Kubernetes.Endpoint, Namespace: m.Kubernetes.Namespace, Kubeconfig: m.Kubernetes.Kubeconfig,
		owner: m.Owner, client: &http.Client{
			Timeout:       5 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
			Transport: &http.Transport{
				TLSClientConfig:   &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12},
				DisableKeepAlives: true,
			},
		},
	}, nil
}

// Ready proves the real API, one Ready node, and the namespace's exact owner.
// TLS failures and missing resources fail before any application scenario starts.
func (k *Kubernetes) Ready(ctx context.Context) error {
	if err := k.read(ctx, "/readyz", nil); err != nil {
		return err
	}
	var nodes struct {
		Items []struct {
			Status struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := k.read(ctx, "/api/v1/nodes", &nodes); err != nil {
		return err
	}
	ready := false
	if len(nodes.Items) == 1 {
		for _, c := range nodes.Items[0].Status.Conditions {
			ready = ready || c.Type == "Ready" && c.Status == "True"
		}
	}
	if !ready {
		return fmt.Errorf("%w: Kubernetes node is not Ready", ErrLab)
	}
	var namespace struct {
		Metadata struct {
			Name        string            `json:"name"`
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}
	if err := k.read(ctx, "/api/v1/namespaces/"+k.Namespace, &namespace); err != nil {
		return err
	}
	if namespace.Metadata.Name != k.Namespace || namespace.Metadata.Annotations["io.infraweave.lab.owner"] != k.owner {
		return fmt.Errorf("%w: Kubernetes namespace ownership", ErrLab)
	}
	return nil
}

func (k *Kubernetes) read(ctx context.Context, path string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.Endpoint+path, nil)
	if err != nil {
		return fmt.Errorf("%w: Kubernetes readiness request", ErrLab)
	}
	resp, err := k.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: Kubernetes API is unreachable or TLS refused", ErrLab)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: Kubernetes API readiness status %d", ErrLab, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return fmt.Errorf("%w: Kubernetes readiness response bound", ErrLab)
	}
	if target != nil && json.Unmarshal(data, target) != nil {
		return fmt.Errorf("%w: Kubernetes readiness response", ErrLab)
	}
	return nil
}
