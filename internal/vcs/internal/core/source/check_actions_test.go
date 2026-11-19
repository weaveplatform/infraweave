package source

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

type actionBindings struct {
	first, next          port.Integration
	reads                int
	metadata             int
	missing, metaMissing bool
	useErr, metaErr      error
}

func (b *actionBindings) Use(context.Context, kernel.AccountID, string, *kernel.ULID) (port.Integration, bool, error) {
	b.reads++
	if b.missing || b.useErr != nil {
		return port.Integration{}, false, b.useErr
	}
	if b.reads == 1 {
		return b.first, true, nil
	}
	return b.next, true, nil
}

func (b *actionBindings) Binding(context.Context, kernel.AccountID, string, *kernel.ULID) (port.Integration, bool, error) {
	b.metadata++
	return b.first, !b.metaMissing, b.metaErr
}

type actionNative struct {
	port.Source
	readApp, permissionApp string
	calls                  int
	resolution             *port.Resolution
	readErr, permissionErr error
}

func (p *actionNative) CheckHost(s port.Settings) (string, error) {
	return s.APIHost + "/app/" + s.AppID, nil
}

func (p *actionNative) ResolveRepositoryRef(_ context.Context, r port.Repository, ref string) (port.Resolution, error) {
	p.readApp = r.Integration.Settings.AppID
	p.calls++
	if p.resolution != nil {
		return *p.resolution, p.readErr
	}
	return port.Resolution{Commit: vcsapi.Commit{Hash: ref}, Repository: vcsapi.Repository{ID: "7", Namespace: r.Namespace, Name: r.Name}}, p.readErr
}

func (p *actionNative) CanWrite(_ context.Context, r port.Repository, _ string) (bool, error) {
	p.permissionApp = r.Integration.Settings.AppID
	p.calls++
	return p.permissionApp == "2", p.permissionErr
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

func actionAuthority() (*Service, *actionBindings, *actionNative, vcsapi.Event, port.ActionFacts) {
	in := port.Integration{ID: kernel.NewULID(), Provider: "GITHUB_ENTERPRISE", Settings: port.Settings{AppID: "1", APIHost: "https://github.example/api/v3"}}
	bindings := &actionBindings{first: in, next: in}
	native := &actionNative{}
	service := New(Deps{Integrations: bindings, Providers: map[string]port.Source{in.Provider: native}})
	event := vcsapi.Event{
		Type:        "check_action",
		Head:        strings.Repeat("a", 40),
		Author:      "developer",
		Repository:  vcsapi.Repository{ID: "7", Namespace: "acme", Name: "infra"},
		CheckAction: &vcsapi.CheckAction{Action: "deploy"},
	}
	return service, bindings, native, event, port.ActionFacts{NativeHost: in.Settings.APIHost + "/app/1"}
}

func TestUnitCheckActionNativeFactsFenceAuthority(t *testing.T) {
	for _, name := range []string{"repository replaced", "immutable SHA changed", "installation removed", "integration gone", "credential unavailable", "host retargeted", "native unavailable", "native cancelled", "permission limited"} {
		t.Run(name, func(t *testing.T) {
			service, bindings, native, event, facts := actionAuthority()
			want := port.ErrCheckActionTarget
			calls := 1
			switch name {
			case "repository replaced":
				native.resolution = &port.Resolution{Commit: vcsapi.Commit{Hash: event.Head}, Repository: vcsapi.Repository{ID: "8"}}
			case "immutable SHA changed":
				native.resolution = &port.Resolution{Commit: vcsapi.Commit{Hash: strings.Repeat("b", 40)}, Repository: event.Repository}
			case "installation removed":
				want = &kernel.Invalid{Detail: "installation removed"}
				service.identities = &identityGuard{err: want}
				calls = 0
			case "integration gone":
				bindings.missing = true
				calls = 0
			case "credential unavailable":
				want = errOffline
				bindings.useErr = want
				calls = 0
			case "host retargeted":
				bindings.first.Settings.APIHost = "https://other.example/api/v3"
			case "native unavailable":
				want = errOffline
				native.readErr = want
			case "native cancelled":
				want = context.Canceled
				native.readErr = want
			case "permission limited":
				want = &port.RateLimited{Reset: time.Now().Add(time.Minute)}
				native.permissionErr = want
				calls = 2
			}
			allowed, err := service.RefreshCheckAction(t.Context(), kernel.NewAccountID(), bindings.first.ID, bindings.first.Provider, event, facts)
			if allowed || !errors.Is(err, want) || native.calls != calls {
				t.Fatal("changed source authority or failure authorized a press", allowed, err, native.calls)
			}
			if name != "permission limited" && native.permissionApp != "" {
				t.Fatal("invalid source reached collaborator lookup", native.permissionApp)
			}
			if name == "permission limited" {
				if limited, ok := errors.AsType[*port.RateLimited](err); !ok || !errors.Is(limited, want) {
					t.Fatal("native scheduling signal lost", err)
				}
			}
		})
	}
}

func TestUnitCheckActionTransactionBoundaryAndMetadataOnlyGate(t *testing.T) {
	service, bindings, native, event, facts := actionAuthority()
	acct := kernel.NewAccountID()
	if err := tx.Do(t.Context(), aliasDB{}, tx.Options{}, func(ctx context.Context) error {
		if _, err := service.PrepareCheckAction(ctx, acct, bindings.first.ID, bindings.first.Provider, event); !errors.Is(err, port.ErrCheckActionTarget) {
			t.Fatal("native preparation accepted inherited transaction", err)
		}
		if _, err := service.RefreshCheckAction(ctx, acct, bindings.first.ID, bindings.first.Provider, event, facts); !errors.Is(err, port.ErrCheckActionTarget) {
			t.Fatal("native authorization accepted inherited transaction", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if bindings.reads != 0 || native.calls != 0 {
		t.Fatal("caller transaction reached credential/provider", bindings.reads, native.calls)
	}
	if current, err := service.CurrentCheckAction(
		t.Context(),
		acct,
		bindings.first.ID,
		bindings.first.Provider,
		event.Repository,
		facts,
	); current ||
		!errors.Is(err, port.ErrCheckActionTarget) {
		t.Fatal("metadata gate accepted missing completion transaction", current, err)
	}
	for _, name := range []string{"live metadata", "integration gone", "metadata unavailable", "host changed", "installation removed", "installation state unavailable"} {
		t.Run(name, func(t *testing.T) {
			service, bindings, native, event, facts := actionAuthority()
			bindings.useErr = errors.New("opening credentials is forbidden under completion locks")
			var want error
			switch name {
			case "integration gone":
				bindings.metaMissing = true
			case "metadata unavailable":
				want = errOffline
				bindings.metaErr = want
			case "host changed":
				bindings.first.Settings.APIHost = "https://other.example/api/v3"
			case "installation removed":
				service.identities = &identityGuard{err: &kernel.Invalid{Detail: "repository membership removed"}}
			case "installation state unavailable":
				want = errOffline
				service.identities = &identityGuard{err: want}
			}
			var current bool
			err := tx.Do(t.Context(), aliasDB{}, tx.Options{}, func(ctx context.Context) error {
				var err error
				current, err = service.CurrentCheckAction(ctx, acct, bindings.first.ID, bindings.first.Provider, event.Repository, facts)
				return err
			})
			if current != (name == "live metadata") || !errors.Is(err, want) || bindings.reads != 0 || bindings.metadata != 1 || native.calls != 0 {
				t.Fatal("current metadata escaped identity/error/no-native boundary", current, err, bindings.reads, bindings.metadata, native.calls)
			}
		})
	}
}
