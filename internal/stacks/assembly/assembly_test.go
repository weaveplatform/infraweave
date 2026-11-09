package assembly

import (
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/pg"
)

// TestUnitDomain checks that the domain's stacksapi.Stacks, its space
// guard and its subject lookup are one service, that its stream is the
// stacks stream, and that its event kind registers once.
func TestUnitDomain(t *testing.T) {
	d := New(Deps{})
	if d.Stacks() == nil || d.SpaceGuard() == nil || any(d.Stacks()) != any(d.SpaceGuard()) {
		t.Errorf("Stacks = %v, SpaceGuard = %v", d.Stacks(), d.SpaceGuard())
	}
	if d.SpaceOf() == nil {
		t.Error("SpaceOf is nil")
	}
	if got := Stream().Name; got != pg.StreamName {
		t.Errorf("Stream().Name = %q, want %q", got, pg.StreamName)
	}
	if d.API() == (StacksAPI{}) {
		t.Error("API is the zero partial")
	}
	if (*Domain)(nil).API() != (StacksAPI{}) {
		t.Error("a nil domain's API is not the zero partial")
	}
	kinds := outbox.NewKinds()
	if err := RegisterEvents(kinds); err != nil {
		t.Fatal(err)
	}
	doc := []byte(`{"stack":"01JC0000000000000000000000","slug":"infra","change":"created"}`)
	if err := kinds.Validate(stacksapi.ChangedKind, stacksapi.ChangedVersion, doc); err != nil {
		t.Errorf("a Changed document fails its schema: %v", err)
	}
	if err := RegisterEvents(kinds); !errors.Is(err, outbox.ErrKind) {
		t.Errorf("a second registration = %v", err)
	}
}
