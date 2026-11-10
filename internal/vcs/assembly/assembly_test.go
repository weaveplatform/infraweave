package assembly

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// stacks answers Settings with a stack on raw Git.
type stacks struct{ stacksapi.Stacks }

func (stacks) Settings(context.Context, kernel.AccountID, kernel.ULID) (stacksapi.Settings, error) {
	return stacksapi.Settings{Provider: "GIT"}, nil
}

// TestUnitDomain checks that the domain reads no provider's source yet,
// so a stack's ref is refused as invalid, and that a nil domain's partial
// is the zero one.
func TestUnitDomain(t *testing.T) {
	d := New(Deps{Stacks: stacks{}})
	_, err := d.Sources().Resolve(t.Context(), kernel.NewAccountID(), kernel.NewULID(), "main")
	if _, ok := errors.AsType[*kernel.Invalid](err); !ok {
		t.Errorf("Resolve = %v", err)
	}
	var none *Domain
	if !reflect.ValueOf(none.API()).IsZero() {
		t.Error("a nil domain's partial is not the zero one")
	}
}
