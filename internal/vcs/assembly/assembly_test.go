package assembly

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// stacks answers Settings with a stack on provider, at an http URL.
type stacks struct {
	stacksapi.Stacks
	provider string
}

func (s stacks) Settings(
	context.Context,
	kernel.AccountID,
	kernel.ULID,
) (stacksapi.Settings, error) {
	u := "http://git.example/acme/infra.git"
	return stacksapi.Settings{Provider: s.provider, RepositoryURL: &u}, nil
}

// TestUnitDomain checks that the domain reads raw Git, refusing an http
// URL at /repositoryURL before any request; reads GitHub, refusing a
// stack with no integration at /vcsIntegrationId; reads no other provider
// yet; and that a nil domain's partial is the zero one.
func TestUnitDomain(t *testing.T) {
	for provider, pointer := range map[string]string{
		"GIT": "/repositoryURL", "GITHUB": "/vcsIntegrationId", "GITLAB": "/provider",
	} {
		d, err := New(Deps{Stacks: stacks{provider: provider}})
		if err != nil {
			t.Fatal(err)
		}
		_, err = d.Sources().Resolve(t.Context(), kernel.NewAccountID(), kernel.NewULID(), "main")
		if inv, ok := errors.AsType[*kernel.Invalid](err); !ok || inv.Errors[0].Pointer != pointer {
			t.Errorf("Resolve on %s = %v", provider, err)
		}
	}
	var none *Domain
	if !reflect.ValueOf(none.API()).IsZero() {
		t.Error("a nil domain's partial is not the zero one")
	}
}
