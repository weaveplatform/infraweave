package assembly

import (
	"context"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// TestUnitStackChanged checks that a document that does not decode never
// reaches the autoattach and kills its job at once, and that one that does
// runs it on the event's account and the document's stack.
func TestUnitStackChanged(t *testing.T) {
	var got []kernel.ULID
	h := stackChanged(func(_ context.Context, acct kernel.AccountID, stack kernel.ULID) error {
		got = append(got, acct.ULID, stack)
		return nil
	})
	if err := h(t.Context(), outbox.Event{Kind: stacksapi.ChangedKind, Document: []byte(`[]`)},
		queue.Job{}); err == nil || got != nil {
		t.Errorf("a bad document = %v, ran on %v", err, got)
	}
	acct, stack := kernel.NewULID(), kernel.NewULID()
	doc := []byte(`{"stack":"` + stack.String() + `","slug":"web","change":"updated"}`)
	if err := h(t.Context(), outbox.Event{Account: acct, Document: doc}, queue.Job{}); err != nil ||
		len(got) != 2 || got[0] != acct || got[1] != stack {
		t.Errorf("a document = %v, ran on %v", err, got)
	}
}
