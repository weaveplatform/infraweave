package http

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/core"
)

// credentialReader names the computed variables of each stack in names,
// or fails with err.
type credentialReader struct {
	names map[kernel.ULID][]string
	err   error
}

func (c credentialReader) CredentialVariables(context.Context, kernel.AccountID, []kernel.ULID) (map[kernel.ULID][]string, error) {
	return c.names, c.err
}

// storedConfig answers one stored variable for every stack.
type storedConfig struct{ unbound }

func (storedConfig) RuntimeConfig(_ context.Context, _ kernel.AccountID, stacks []kernel.ULID) (map[kernel.ULID][]stacksapi.ConfigElementWithSource, error) {
	out := map[kernel.ULID][]stacksapi.ConfigElementWithSource{}
	for _, id := range stacks {
		out[id] = []stacksapi.ConfigElementWithSource{
			{ConfigElement: stacksapi.ConfigElement{ID: "AWS_SESSION_TOKEN", Value: new("mine")}},
			{ConfigElement: stacksapi.ConfigElement{ID: "ZONE", Value: new("eu")}},
		}
	}
	return out, nil
}

// TestUnitRuntimeConfigCredentials checks that a stack's runtimeConfig
// lists each computed credential variable its cloud integrations set as a
// computed write-only element, by ID, unless a stored variable of its name
// replaces it, and that a failed read fails the view.
func TestUnitRuntimeConfigCredentials(t *testing.T) {
	t.Parallel()
	server := New(core.New(core.Deps{Store: requestViewStore{}}), requestViewAuth{}, requestViewSpaces{}, nil)
	server.BindConfiguration(storedConfig{})
	account := kernel.NewAccountID()
	attached, bare := kernel.NewULID(), kernel.NewULID()
	stacks := []core.Stack{
		{Settings: stacksapi.Settings{Stack: stacksapi.Stack{ID: attached, Slug: "a", Space: account.ULID}, VendorConfig: []byte(`{}`)}},
		{Settings: stacksapi.Settings{Stack: stacksapi.Stack{ID: bare, Slug: "b", Space: account.ULID}, VendorConfig: []byte(`{}`)}},
	}
	names := []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SECURITY_TOKEN", "AWS_SESSION_TOKEN"}
	server.BindCredentials(credentialReader{names: map[kernel.ULID][]string{attached: names}})
	views, err := server.views(t.Context(), kernel.Principal{AccountID: account}, stacks...)
	if err != nil || len(views) != 2 {
		t.Fatal(views, err)
	}
	ids := make([]string, 0, len(views[0].RuntimeConfig))
	for _, e := range views[0].RuntimeConfig {
		ids = append(ids, e.Element.Id)
		computed := e.Element.Id != "AWS_SESSION_TOKEN" && e.Element.Id != "ZONE"
		if e.Element.Runtime != computed || e.Element.WriteOnly != computed || (computed && e.Element.Value != nil) {
			t.Errorf("%s = %+v", e.Element.Id, e.Element)
		}
	}
	if !slices.Equal(ids, []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SECURITY_TOKEN", "AWS_SESSION_TOKEN", "ZONE"}) {
		t.Errorf("attached stack's elements %v", ids)
	}
	if len(views[1].RuntimeConfig) != 2 || views[1].RuntimeConfig[0].Element.Id != "AWS_SESSION_TOKEN" {
		t.Errorf("bare stack's elements %+v", views[1].RuntimeConfig)
	}
	broken := errors.New("cloud unavailable")
	server.BindCredentials(credentialReader{err: broken})
	if _, err := server.views(t.Context(), kernel.Principal{AccountID: account}, stacks...); !errors.Is(err, broken) {
		t.Fatalf("err = %v", err)
	}
}
