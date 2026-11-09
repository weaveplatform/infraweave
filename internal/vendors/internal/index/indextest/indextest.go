package indextest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/iac/version"
	"github.com/weaveplatform/infraweave/internal/vendors/internal/index"
)

// Facts are what a reader of an index must find in it: releases it lists,
// and the release each constraint resolves to.
type Facts struct {
	Listed   []string
	Resolves []Resolution
}

// Resolution is a constraint and the release it resolves to.
type Resolution struct{ Constraints, Version string }

// The facts of the vendors' real indexes, true of the recorded fixtures
// too. They name lines the vendors no longer release into, so no new
// release changes them.
var (
	TerraformFacts = Facts{
		Listed: []string{"1.5.7", "1.6.0"},
		Resolves: []Resolution{
			{">= 1.5.0, < 1.6.0", "1.5.7"},
			{"~> 1.5.0", "1.5.7"},
			{"1.6.0", "1.6.0"},
		},
	}
	OpenTofuFacts = Facts{
		Listed:   []string{"1.6.0"},
		Resolves: []Resolution{{"< 1.6.1", "1.6.0"}, {"= 1.6.0", "1.6.0"}},
	}
)

// ErrContract is a reader that breaks the contract.
var ErrContract = errors.New("index contract broken")

// none is a constraint no release of either vendor satisfies.
const none = "> 999.0.0"

// Check reads r's index and checks it against the contract every reader
// keeps and against f: it lists releases, every one stable, newest first
// and each once; it lists f's releases; each of f's constraints resolves
// to its release; and a constraint no release satisfies resolves to none.
func Check(ctx context.Context, r index.Reader, f Facts) error {
	rs, err := r.Releases(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrContract, err)
	}
	if len(rs) == 0 {
		return fmt.Errorf("%w: no release listed", ErrContract)
	}
	var errs []error
	for i, rel := range rs {
		if rel.Version.Prerelease != "" {
			errs = append(errs, fmt.Errorf("%w: prerelease %s listed", ErrContract, rel.Version))
		}
		if i > 0 && rs[i-1].Version.Compare(rel.Version) <= 0 {
			errs = append(errs, fmt.Errorf("%w: %s listed after %s, not newest first",
				ErrContract, rel.Version, rs[i-1].Version))
		}
	}
	listed := make([]string, len(rs))
	for i, rel := range rs {
		listed[i] = rel.Version.String()
	}
	for _, v := range f.Listed {
		if !slices.Contains(listed, v) {
			errs = append(errs, fmt.Errorf("%w: %s not listed", ErrContract, v))
		}
	}
	for _, res := range append(slices.Clone(f.Resolves), Resolution{Constraints: none}) {
		cs, err := version.ParseConstraints(res.Constraints)
		if err != nil {
			errs = append(errs, fmt.Errorf("%w: %w", ErrContract, err))
			continue
		}
		got := ""
		if rel, ok := index.Resolve(rs, cs); ok {
			got = rel.Version.String()
		}
		if got != res.Version {
			errs = append(errs, fmt.Errorf("%w: %q resolves to %q, want %q",
				ErrContract, res.Constraints, got, res.Version))
		}
	}
	return errors.Join(errs...)
}

// Run fails t with Check's every break of the contract.
func Run(t *testing.T, r index.Reader, f Facts) {
	t.Helper()
	if err := Check(t.Context(), r, f); err != nil {
		t.Fatal(err)
	}
}

// Fixture is a recorded index, as make vendors-fixtures writes it: where
// it came from, when, and its body as the index serves it, trimmed to the
// releases kept.
type Fixture struct {
	Source  string          `json:"source"`
	Fetched string          `json:"fetched"`
	Body    json.RawMessage `json:"body"`
}

// Load reads the fixture at path.
func Load(path string) (Fixture, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // a test's own fixture
	if err != nil {
		return Fixture{}, fmt.Errorf("fixture: %w", err)
	}
	var f Fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		return Fixture{}, fmt.Errorf("fixture %s: %w", path, err)
	}
	return f, nil
}

// HashiCorp serves body, a recorded page of HashiCorp's releases API
// newest first, as the API pages it: limit releases at most, created
// before after's time when it names one.
func HashiCorp(body json.RawMessage) http.Handler {
	r := chi.NewRouter()
	r.Get("/*", func(w http.ResponseWriter, req *http.Request) {
		var releases []map[string]any
		if err := json.Unmarshal(body, &releases); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		limit, err := strconv.Atoi(req.URL.Query().Get("limit"))
		if err != nil || limit < 1 || limit > index.HashiCorpPage {
			http.Error(w, "limit is 1 to 20", http.StatusBadRequest)
			return
		}
		out := []map[string]any{}
		if after := req.URL.Query().Get("after"); after != "" {
			cut, err := time.Parse(time.RFC3339Nano, after)
			if err != nil {
				http.Error(w, "after is a time", http.StatusBadRequest)
				return
			}
			for _, rel := range releases {
				created, _ := rel["timestamp_created"].(string)
				if t, err := time.Parse(time.RFC3339Nano, created); err == nil && t.Before(cut) {
					out = append(out, rel)
				}
			}
		} else {
			out = releases
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out[:min(limit, len(out))]) // a client gone has no answer
	})
	return r
}

// OpenTofu serves body, a recorded OpenTofu index, whole.
func OpenTofu(body json.RawMessage) http.Handler {
	r := chi.NewRouter()
	r.Get("/*", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body) // as above
	})
	return r
}
