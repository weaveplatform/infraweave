package deps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
)

// EnvLabWorkers names the combined managed cloud worker manifest that a
// managed cloud batch of test/terraform/lab writes beside its platform
// manifest. The coordinator validates every field before execution; the suite
// repeats the ownership and census checks so a stale or foreign file fails.
const EnvLabWorkers = "INFRAWEAVE_LAB_WORKERS"

// WorkerHosting is one cloud's attached, non-secret worker installation.
type WorkerHosting struct {
	Provider  string
	ClusterID string
	Namespace string
	Pools     int
}

type workersManifest struct {
	Schema  int    `json:"schema"`
	Owner   string `json:"owner"`
	Batch   string `json:"batch"`
	Hosting []struct {
		Owner string `json:"owner"`
		Batch string `json:"batch"`
		Cloud struct {
			Provider string `json:"provider"`
		} `json:"cloud"`
		Cluster struct {
			ID string `json:"id"`
		} `json:"cluster"`
		Namespace struct {
			Name string `json:"name"`
			UID  string `json:"uid"`
		} `json:"namespace"`
		Pools []json.RawMessage `json:"pools"`
	} `json:"hosting"`
}

// readWorkers decodes the envelope strictly and the hosting entries by the
// fields the suite relies on; the coordinator owns their complete schema.
func readWorkers(data []byte, owner, batch string) ([]WorkerHosting, error) {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(data, &envelope) != nil || len(envelope) != 4 {
		return nil, fmt.Errorf("%w: malformed workers manifest", ErrLab)
	}
	for _, key := range []string{"schema", "owner", "batch", "hosting"} {
		if _, ok := envelope[key]; !ok {
			return nil, fmt.Errorf("%w: malformed workers manifest", ErrLab)
		}
	}
	var m workersManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&m); err != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("%w: malformed workers manifest", ErrLab)
	}
	if m.Schema != 1 || m.Owner != owner || m.Batch != batch || len(m.Hosting) == 0 {
		return nil, fmt.Errorf("%w: workers manifest belongs to another batch", ErrLab)
	}
	var hosting []WorkerHosting
	for _, entry := range m.Hosting {
		provider := entry.Cloud.Provider
		if entry.Owner != owner || entry.Batch != batch || !slices.Contains([]string{"aws", "azure", "google"}, provider) ||
			slices.ContainsFunc(hosting, func(h WorkerHosting) bool { return h.Provider == provider }) {
			return nil, fmt.Errorf("%w: workers manifest hosting ownership or provider", ErrLab)
		}
		if entry.Cluster.ID == "" || entry.Namespace.Name == "" || entry.Namespace.UID == "" || len(entry.Pools) == 0 {
			return nil, fmt.Errorf("%w: workers manifest hosting is incomplete", ErrLab)
		}
		hosting = append(hosting, WorkerHosting{
			Provider: provider, ClusterID: entry.Cluster.ID, Namespace: entry.Namespace.Name, Pools: len(entry.Pools),
		})
	}
	return hosting, nil
}

// WorkerHosting reads the managed cloud worker manifest that belongs to the
// borrowed lab deployment. It fails, and never provisions a substitute, when
// the batch attached no cloud or the manifest names another owner.
func (d *Dependencies) WorkerHosting() ([]WorkerHosting, error) {
	file := os.Getenv(EnvLabWorkers)
	if d.lab == nil || file == "" {
		return nil, fmt.Errorf("%w: this batch attached no managed cloud workers", ErrLab)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("%w: read workers manifest: %w", ErrLab, err)
	}
	return readWorkers(raw, d.lab.Owner, d.lab.Batch)
}
