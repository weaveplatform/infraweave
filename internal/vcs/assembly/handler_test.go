package assembly

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// TestUnitStackChanged checks that the handler refuses a document that
// does not decode, skips a change that leaves the stack's source, and
// tracks the stack of one that gives or changes it.
func TestUnitStackChanged(t *testing.T) {
	var got []kernel.ULID
	h := stackChanged(func(_ context.Context, _ kernel.AccountID, stack kernel.ULID) error {
		got = append(got, stack)
		return nil
	})
	if err := h(t.Context(), outbox.Event{Document: []byte(`[]`)}, queue.Job{}); err == nil {
		t.Error("a document that does not decode was handled")
	}
	id := kernel.NewULID()
	for _, source := range []bool{false, true} {
		doc, err := json.Marshal(
			stacksapi.Changed{Stack: id, Slug: "infra", Change: "updated", Source: source},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := h(t.Context(), outbox.Event{Document: doc}, queue.Job{}); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 1 || got[0] != id {
		t.Errorf("tracked %v, want the source change's stack alone", got)
	}
}

// TestUnitCAFile checks that vcs.ca_file is read at construction, and
// that a missing file or one with no certificate fails it.
func TestUnitCAFile(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	srv.Close()
	good := filepath.Join(dir, "ca.pem")
	bad := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(good, pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: srv.Certificate().Raw,
	}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Deps{Settings: Settings{CAFile: good}}); err != nil {
		t.Errorf("New with a CA file = %v", err)
	}
	for _, f := range []string{bad, filepath.Join(dir, "missing.pem")} {
		if _, err := New(Deps{Settings: Settings{CAFile: f}}); !errors.Is(err, ErrConfig) {
			t.Errorf("New with %s = %v", filepath.Base(f), err)
		}
	}
	d, err := New(Deps{})
	if err != nil || d.StackSources() == nil {
		t.Errorf("New = %v", err)
	}
}
