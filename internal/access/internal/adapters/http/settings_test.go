package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/settings"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// configs is settings.Store in memory for one account: err fails every
// call, and gone makes the account not exist.
type configs struct {
	s    settings.Settings
	err  error
	gone bool
	// requests and deletes count the recorded request and deletions.
	requests, deletes int
}

func (c *configs) fail() error {
	if c.gone {
		return fmt.Errorf("%w: account", port.ErrNotFound)
	}
	return c.err
}

func (c *configs) Settings(context.Context, kernel.AccountID) (settings.Settings, error) {
	return c.s, c.fail()
}

func (c *configs) SetPersonalAPIKeys(
	_ context.Context,
	_ kernel.AccountID,
	enabled, _ bool,
) ([]kernel.ULID, error) {
	c.s.PersonalAPIKeys = enabled
	return nil, c.fail()
}

func (c *configs) ToggleNonHumanKeyManagers(context.Context, kernel.AccountID) (bool, error) {
	c.s.NonHumanKeyManagers = !c.s.NonHumanKeyManagers
	return c.s.NonHumanKeyManagers, c.fail()
}

func (c *configs) SetSecurityEmail(_ context.Context, _ kernel.AccountID, e string) error {
	c.s.SecurityEmail = e
	return c.fail()
}

func (c *configs) SetRedirectHosts(_ context.Context, _ kernel.AccountID, h []string) error {
	c.s.RedirectHosts = h
	return c.fail()
}

func (c *configs) SetSurvey(_ context.Context, _ kernel.AccountID, sv settings.Survey) error {
	c.s.Survey = sv
	return c.fail()
}

func (c *configs) ToggleDeletionMark(
	_ context.Context,
	_ kernel.AccountID,
	m settings.Mark,
) (bool, error) {
	if c.s.Mark == nil {
		c.s.Mark = &m
	} else {
		c.s.Mark = nil
	}
	return c.s.Mark != nil, c.fail()
}

func (c *configs) RequestPersonalAPIKeys(context.Context, kernel.AuditEntry) error {
	c.requests++
	return c.fail()
}

func (c *configs) DeleteAccount(
	context.Context,
	kernel.AccountID,
	kernel.OrganizationAuditEntry,
) ([]kernel.ULID, error) {
	switch {
	case c.s.Mark == nil:
		return nil, settings.ErrInvalidState
	case c.fail() != nil:
		return nil, c.fail()
	}
	c.deletes++
	return nil, nil
}

// TestUnitSettings runs the account settings through the router with a
// key's token: each reads its default, is set and read back, and a value
// the service refuses answers 422.
func TestUnitSettings(t *testing.T) {
	w := newWorld(t)
	bearer := "Bearer " + w.exchange(t).Jwt
	get := func(path string) string {
		t.Helper()
		rec := w.do(t, http.MethodGet, path, bearer, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body)
		}
		return rec.Body.String()
	}
	post := func(path, body string, code int) string {
		t.Helper()
		rec := w.do(t, http.MethodPost, path, bearer, body)
		if rec.Code != code {
			t.Errorf("POST %s %s: %d %s, want %d", path, body, rec.Code, rec.Body, code)
		}
		return rec.Body.String()
	}
	for path, want := range map[string]string{
		"/personal-api-keys-enabled":         "true\n",
		"/api-keys-manageable-by-non-humans": "false\n",
		"/api-keys-managed-by-non-admins":    "false\n",
		"/security-email":                    "\"\"\n",
		"/oauth-redirect-allowed-hosts":      "{\"items\":[]}\n",
		"/awareness-source-survey":           "{\"completed\":false}\n",
		"/marked-for-deletion":               "false\n",
		"/account/can-be-deleted":            "false\n",
		"/account/can-be-deleted-at":         "0\n",
	} {
		if got := get(path); got != want {
			t.Errorf("GET %s = %q, want %q", path, got, want)
		}
	}
	post("/account/set-personal-api-keys-enabled", `{"enabled":false,"disableExistingKeys":true}`,
		http.StatusOK)
	post("/account/request-personal-api-keys-enabling", "", http.StatusOK)
	post("/account/update-security-email", `{"securityEmail":"sec@example.com"}`, http.StatusOK)
	post("/account/update-security-email", `{"securityEmail":"nope"}`,
		http.StatusUnprocessableEntity)
	post("/account/set-o-auth-redirect-allowlist", `{"hosts":["A.example.com","*.b.example.com"]}`,
		http.StatusOK)
	if p := post("/account/set-o-auth-redirect-allowlist", `{"hosts":["https://c.example.com"]}`,
		http.StatusUnprocessableEntity); !strings.Contains(p, "hosts[0]") {
		t.Errorf("a refused host's problem does not name it: %s", p)
	}
	post("/account/update-awareness-source-survey", `{"completed":true,"response":"a friend"}`,
		http.StatusOK)
	// A key flips nothing that lets keys manage keys.
	if p := post("/account/toggle-api-key-management-from-non-humans", "",
		http.StatusForbidden); !strings.Contains(p, "forbidden") {
		t.Errorf("a key's toggle: %s", p)
	}
	w.cfg.s.NonAdminKeyManagers = true
	for path, want := range map[string]string{
		"/personal-api-keys-enabled":         "false\n",
		"/api-keys-manageable-by-non-humans": "false\n",
		"/api-keys-managed-by-non-admins":    "true\n",
		"/security-email":                    "\"sec@example.com\"\n",
		"/awareness-source-survey":           "{\"completed\":true,\"response\":\"a friend\"}\n",
	} {
		if got := get(path); got != want {
			t.Errorf("GET %s = %q, want %q", path, got, want)
		}
	}
	if w.cfg.requests != 1 {
		t.Errorf("%d requests recorded", w.cfg.requests)
	}
}

// TestUnitRedirectHostsPages pages through the allowlist one host at a
// time, and refuses a cursor whose host was removed since.
func TestUnitRedirectHostsPages(t *testing.T) {
	w := newWorld(t)
	bearer := "Bearer " + w.exchange(t).Jwt
	w.cfg.s.RedirectHosts = []string{"c.example.com", "a.example.com", "*.b.example.com"}
	var got []string
	target := "/oauth-redirect-allowed-hosts?limit=1"
	for range 4 {
		rec := w.do(t, http.MethodGet, target, bearer, "")
		pg := decode[api.OauthRedirectAllowedHostsPage](t, rec)
		got = append(got, pg.Items...)
		if pg.NextCursor == nil {
			break
		}
		target = "/oauth-redirect-allowed-hosts?limit=1&cursor=" + *pg.NextCursor
	}
	if fmt.Sprint(got) != fmt.Sprint(w.cfg.s.RedirectHosts) {
		t.Fatalf("paged hosts = %v", got)
	}
	rec := w.do(t, http.MethodGet, "/oauth-redirect-allowed-hosts?limit=1", bearer, "")
	next := *decode[api.OauthRedirectAllowedHostsPage](t, rec).NextCursor
	w.cfg.s.RedirectHosts = []string{"a.example.com"}
	for _, cursor := range []string{next, "garbage"} {
		rec = w.do(t, http.MethodGet, "/oauth-redirect-allowed-hosts?cursor="+cursor, bearer, "")
		if p := decode[problem.Problem](t, rec); rec.Code != http.StatusBadRequest ||
			p.Type != problem.InvalidCursor.New("").Type {
			t.Errorf("cursor %q: %d %+v", cursor, rec.Code, p)
		}
	}
}

// TestUnitAccountDeletion marks the account, reads that it can be
// deleted from the mark's time, and deletes it; an unmarked account
// answers 409.
func TestUnitAccountDeletion(t *testing.T) {
	w := newWorld(t)
	bearer := "Bearer " + w.exchange(t).Jwt
	rec := w.do(t, http.MethodPost, "/account/confirm-delete", bearer, "")
	if p := decode[problem.Problem](t, rec); rec.Code != http.StatusConflict ||
		p.Type != problem.InvalidState.New("").Type {
		t.Errorf("deleting an unmarked account: %d %+v", rec.Code, p)
	}
	rec = w.do(t, http.MethodPost, "/account/toggle-deletion-mark", bearer,
		`{"reason":"moving","description":"to another account"}`)
	if rec.Code != http.StatusOK || rec.Body.String() != "true\n" || w.cfg.s.Mark == nil ||
		*w.cfg.s.Mark.Reason != "moving" || *w.cfg.s.Mark.Description != "to another account" {
		t.Fatalf("mark: %d %s %+v", rec.Code, rec.Body, w.cfg.s.Mark)
	}
	for path, want := range map[string]string{
		"/marked-for-deletion":       "true\n",
		"/account/can-be-deleted":    "true\n",
		"/account/can-be-deleted-at": fmt.Sprintf("%d\n", t0.Unix()),
	} {
		if rec := w.do(t, http.MethodGet, path, bearer, ""); rec.Body.String() != want {
			t.Errorf("GET %s = %q, want %q", path, rec.Body, want)
		}
	}
	if rec := w.do(t, http.MethodPost, "/account/confirm-delete", bearer, ""); rec.Code !=
		http.StatusOK || rec.Body.String() != "true\n" || w.cfg.deletes != 1 {
		t.Errorf("delete: %d %s, %d deletions", rec.Code, rec.Body, w.cfg.deletes)
	}
	// With no body the mark carries no reason, and toggling clears it.
	if rec := w.do(t, http.MethodPost, "/account/toggle-deletion-mark", bearer, ""); rec.Code !=
		http.StatusOK || rec.Body.String() != "false\n" {
		t.Errorf("unmark: %d %s", rec.Code, rec.Body)
	}
}

// TestUnitSettingsRefusals checks the answers when the store is down or
// the account is gone.
func TestUnitSettingsRefusals(t *testing.T) {
	w := newWorld(t)
	bearer := "Bearer " + w.exchange(t).Jwt
	w.cfg.s.Mark = &settings.Mark{At: t0}
	ops := []struct{ method, path, body string }{
		{http.MethodGet, "/personal-api-keys-enabled", ""},
		{http.MethodGet, "/api-keys-manageable-by-non-humans", ""},
		{http.MethodGet, "/api-keys-managed-by-non-admins", ""},
		{http.MethodGet, "/security-email", ""},
		{http.MethodGet, "/oauth-redirect-allowed-hosts", ""},
		{http.MethodGet, "/awareness-source-survey", ""},
		{http.MethodGet, "/marked-for-deletion", ""},
		{http.MethodGet, "/account/can-be-deleted", ""},
		{http.MethodGet, "/account/can-be-deleted-at", ""},
		{http.MethodPost, "/account/set-personal-api-keys-enabled", `{"enabled":true}`},
		{http.MethodPost, "/account/request-personal-api-keys-enabling", ""},
		{http.MethodPost, "/account/update-security-email", `{"securityEmail":""}`},
		{http.MethodPost, "/account/set-o-auth-redirect-allowlist", `{"hosts":[]}`},
		{http.MethodPost, "/account/update-awareness-source-survey", `{"completed":false}`},
		{http.MethodPost, "/account/toggle-deletion-mark", ""},
		{http.MethodPost, "/account/confirm-delete", ""},
	}
	w.cfg.err = fmt.Errorf("down: %w", kernel.ErrUnavailable)
	for _, op := range ops {
		w.cfg.s.Mark = &settings.Mark{At: t0}
		if rec := w.do(
			t,
			op.method,
			op.path,
			bearer,
			op.body,
		); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s with the store down: %d %s", op.method, op.path, rec.Code, rec.Body)
		}
	}
	w.cfg.err, w.cfg.gone = nil, true
	for _, op := range ops {
		w.cfg.s.Mark = &settings.Mark{At: t0}
		if rec := w.do(t, op.method, op.path, bearer, op.body); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s of a gone account: %d %s", op.method, op.path, rec.Code, rec.Body)
		}
	}
}

// TestUnitSettingsNoPrincipal checks that each operation refuses a request
// that reaches it with no principal.
func TestUnitSettingsNoPrincipal(t *testing.T) {
	s := Server{}
	ctx := t.Context()
	for name, call := range map[string]func() error{
		"personal": func() error {
			_, err := s.PersonalApiKeysEnabled(ctx, api.PersonalApiKeysEnabledRequestObject{})
			return err
		},
		"set personal": func() error {
			_, err := s.AccountSetPersonalApiKeysEnabled(ctx,
				api.AccountSetPersonalApiKeysEnabledRequestObject{
					Body: &api.AccountSetPersonalApiKeysEnabledJSONRequestBody{},
				})
			return err
		},
		"request": func() error {
			_, err := s.AccountRequestPersonalApiKeysEnabling(ctx,
				api.AccountRequestPersonalApiKeysEnablingRequestObject{})
			return err
		},
		"non-humans": func() error {
			_, err := s.ApiKeysManageableByNonHumans(ctx, api.ApiKeysManageableByNonHumansRequestObject{})
			return err
		},
		"toggle": func() error {
			_, err := s.AccountToggleAPIKeyManagementFromNonHumans(ctx,
				api.AccountToggleAPIKeyManagementFromNonHumansRequestObject{})
			return err
		},
		"non-admins": func() error {
			_, err := s.ApiKeysManagedByNonAdmins(ctx, api.ApiKeysManagedByNonAdminsRequestObject{})
			return err
		},
		"email": func() error {
			_, err := s.SecurityEmail(ctx, api.SecurityEmailRequestObject{})
			return err
		},
		"set email": func() error {
			_, err := s.AccountUpdateSecurityEmail(ctx, api.AccountUpdateSecurityEmailRequestObject{
				Body: &api.AccountUpdateSecurityEmailJSONRequestBody{},
			})
			return err
		},
		"hosts": func() error {
			_, err := s.OauthRedirectAllowedHosts(ctx, api.OauthRedirectAllowedHostsRequestObject{})
			return err
		},
		"set hosts": func() error {
			_, err := s.AccountSetOAuthRedirectAllowlist(ctx,
				api.AccountSetOAuthRedirectAllowlistRequestObject{
					Body: &api.AccountSetOAuthRedirectAllowlistJSONRequestBody{},
				})
			return err
		},
		"survey": func() error {
			_, err := s.AwarenessSourceSurvey(ctx, api.AwarenessSourceSurveyRequestObject{})
			return err
		},
		"set survey": func() error {
			_, err := s.AccountUpdateAwarenessSourceSurvey(ctx,
				api.AccountUpdateAwarenessSourceSurveyRequestObject{
					Body: &api.AccountUpdateAwarenessSourceSurveyJSONRequestBody{},
				})
			return err
		},
		"marked": func() error {
			_, err := s.MarkedForDeletion(ctx, api.MarkedForDeletionRequestObject{})
			return err
		},
		"mark": func() error {
			_, err := s.AccountToggleDeletionMark(ctx, api.AccountToggleDeletionMarkRequestObject{})
			return err
		},
		"can": func() error {
			_, err := s.AccountCanBeDeleted(ctx, api.AccountCanBeDeletedRequestObject{})
			return err
		},
		"can at": func() error {
			_, err := s.AccountCanBeDeletedAt(ctx, api.AccountCanBeDeletedAtRequestObject{})
			return err
		},
		"delete": func() error {
			_, err := s.AccountConfirmDelete(ctx, api.AccountConfirmDeleteRequestObject{})
			return err
		},
	} {
		if pe, ok := errors.AsType[*problem.Error](call()); !ok ||
			pe.Problem.Status != http.StatusUnauthorized {
			t.Errorf("%s without a principal: %v", name, pe)
		}
	}
}
