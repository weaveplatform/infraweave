package source

import (
	"context"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type actionBindings struct {
	first, next port.Integration
	reads       int
}

func (b *actionBindings) Use(context.Context, kernel.AccountID, string, *kernel.ULID) (port.Integration, bool, error) {
	b.reads++
	if b.reads == 1 {
		return b.first, true, nil
	}
	return b.next, true, nil
}

func (b *actionBindings) Binding(context.Context, kernel.AccountID, string, *kernel.ULID) (port.Integration, bool, error) {
	return b.first, true, nil
}

type actionNative struct {
	port.Source
	readApp, permissionApp string
	calls                  int
}

func (p *actionNative) CheckHost(s port.Settings) (string, error) {
	return s.APIHost + "/app/" + s.AppID, nil
}

func (p *actionNative) ResolveRepositoryRef(_ context.Context, r port.Repository, ref string) (port.Resolution, error) {
	p.readApp = r.Integration.Settings.AppID
	p.calls++
	return port.Resolution{Commit: vcsapi.Commit{Hash: ref}, Repository: vcsapi.Repository{ID: "7", Namespace: r.Namespace, Name: r.Name}}, nil
}

func (p *actionNative) CanWrite(_ context.Context, r port.Repository, _ string) (bool, error) {
	p.permissionApp = r.Integration.Settings.AppID
	p.calls++
	return p.permissionApp == "2", nil
}

func TestUnitCheckActionPermissionUsesResolvedConnection(t *testing.T) {
	t.Parallel()
	// A later read would return App B, whose user has write access, while the
	// final held metadata can again be App A. A's read-only user stays refused.
	a := port.Integration{ID: kernel.NewULID(), Provider: "GITHUB_ENTERPRISE", Settings: port.Settings{AppID: "1", APIHost: "https://github.example/api/v3"}}
	b := a
	b.Settings.AppID = "2"
	bindings := &actionBindings{first: a, next: b}
	native := &actionNative{}
	service := New(Deps{Integrations: bindings, Providers: map[string]port.Source{a.Provider: native}})
	facts := port.ActionFacts{NativeHost: a.Settings.APIHost + "/app/1"}
	event := vcsapi.Event{
		Type:        "check_action",
		Head:        strings.Repeat("a", 40),
		Author:      "developer",
		Repository:  vcsapi.Repository{ID: "7", Namespace: "acme", Name: "infra"},
		CheckAction: &vcsapi.CheckAction{Action: "deploy"},
	}
	allowed, err := service.RefreshCheckAction(t.Context(), kernel.NewAccountID(), a.ID, a.Provider, event, facts)
	if err != nil || allowed || bindings.reads != 1 || native.readApp != "1" || native.permissionApp != "1" {
		t.Fatal("permission came from another connection", allowed, err, bindings.reads, native.readApp, native.permissionApp)
	}
}
