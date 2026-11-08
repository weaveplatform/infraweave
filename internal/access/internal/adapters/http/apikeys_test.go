package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/page"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// The store's apikey.Store: keys of another account, and deleted keys,
// are port.ErrNotFound, and a revoking change revokes the key's sessions.

func (s *store) CreateAPIKey(_ context.Context, k apikey.Key) error {
	if err := s.err(); err != nil {
		return err
	}
	s.keys[k.ID] = k
	return nil
}

func (s *store) AccountAPIKeys(
	_ context.Context,
	acct kernel.AccountID,
	after kernel.ULID,
	limit int,
) ([]apikey.Key, error) {
	var out []apikey.Key
	for _, k := range s.keys {
		if k.AccountID == acct && k.DeletedAt.IsZero() &&
			(after.IsZero() || k.ID.String() > after.String()) {
			out = append(out, k)
		}
	}
	slices.SortFunc(
		out,
		func(a, b apikey.Key) int { return strings.Compare(a.ID.String(), b.ID.String()) },
	)
	return out[:min(len(out), limit)], s.err()
}

func (s *store) AccountAPIKey(
	_ context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (apikey.Key, error) {
	if err := s.err(); err != nil {
		return apikey.Key{}, err
	}
	k, ok := s.keys[id]
	if !ok || k.AccountID != acct || !k.DeletedAt.IsZero() {
		return apikey.Key{}, port.ErrNotFound
	}
	return k, nil
}

func (s *store) UpdateAPIKey(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	c apikey.Change,
) (apikey.Key, error) {
	k, _, err := s.change(ctx, acct, id, func(k *apikey.Key) {
		if c.Name != nil {
			k.Name = *c.Name
		}
		if c.Description != nil {
			k.Description = *c.Description
		}
		if c.Teams != nil {
			k.Teams = *c.Teams
		}
		if c.ClaimMapping != nil {
			k.OIDC.ClaimMapping = *c.ClaimMapping
		}
	}, false)
	return k, err
}

func (s *store) change(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	f func(*apikey.Key),
	revoke bool,
) (apikey.Key, []kernel.ULID, error) {
	k, err := s.AccountAPIKey(ctx, acct, id)
	if err != nil {
		return apikey.Key{}, nil, err
	}
	f(&k)
	s.keys[id] = k
	var ids []kernel.ULID
	for sid, ss := range s.sessions {
		if revoke && ss.ActorID == id && ss.RevokedAt.IsZero() {
			ss.RevokedAt = t0
			s.sessions[sid] = ss
			ids = append(ids, sid)
		}
	}
	return k, ids, nil
}

func (s *store) ResetAPIKey(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	hash []byte,
) (apikey.Key, []kernel.ULID, error) {
	return s.change(ctx, acct, id, func(k *apikey.Key) { k.SecretHash = hash }, true)
}

func (s *store) DisableAPIKey(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	reason string,
) (apikey.Key, []kernel.ULID, error) {
	return s.change(ctx, acct, id, func(k *apikey.Key) {
		k.DisabledAt, k.DisabledReason = t0, reason
	}, true)
}

func (s *store) DeleteAPIKey(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
) (apikey.Key, []kernel.ULID, error) {
	return s.change(ctx, acct, id, func(k *apikey.Key) { k.DeletedAt = t0 }, true)
}

func (s *store) SetNonAdminManagers(
	context.Context,
	kernel.AccountID,
	bool,
) error {
	return s.err()
}

func (s *store) NonHumanKeyManagers(context.Context, kernel.AccountID) (bool, error) {
	return !s.peopleOnly, s.err()
}

func (s *store) NonAdminKeyManagers(context.Context, kernel.AccountID) (bool, error) {
	return false, s.err()
}

// TestUnitAPIKeys runs the API Keys operations through the router: a key
// is created with its secret, read, listed page by page, updated, reset,
// which stops the old secret and its token, and deleted.
func TestUnitAPIKeys(t *testing.T) {
	w := newWorld(t)
	bearer := "Bearer " + w.exchange(t).Jwt

	rec := w.do(
		t,
		http.MethodPost,
		"/api-keys",
		bearer,
		`{"name":"deploy","description":"CI","teams":["ops"],"machine":true,"expiresAt":2000000000}`,
	)
	created := decode[api.ApiKey](t, rec)
	if rec.Code != http.StatusCreated || created.Secret == nil || created.Name != "deploy" ||
		created.Description != "CI" || !created.IsMachineUser || created.Admin ||
		created.Creator != w.key.String() || created.ExpiresAt == nil ||
		*created.ExpiresAt != 2000000000 || created.Type != api.APIKeyTypeSECRET ||
		created.AccessRules == nil || created.TeamCount != 1 || created.OidcSettings != nil {
		t.Fatalf("create: %d %+v", rec.Code, created)
	}
	if rec := w.do(
		t,
		http.MethodGet,
		"/api-keys/"+created.Id,
		bearer,
		"",
	); rec.Code != http.StatusOK ||
		decode[api.ApiKey](t, rec).Secret != nil {
		t.Errorf("read: %d %s", rec.Code, rec.Body)
	}

	first := decode[api.ApiKeyPage](t, w.do(t, http.MethodGet, "/api-keys?limit=1", bearer, ""))
	if len(first.Items) != 1 || first.NextCursor == nil {
		t.Fatalf("first page %+v", first)
	}
	second := decode[api.ApiKeyPage](t,
		w.do(t, http.MethodGet, "/api-keys?limit=1&cursor="+*first.NextCursor, bearer, ""))
	if len(second.Items) != 1 || second.NextCursor != nil ||
		second.Items[0].Id == first.Items[0].Id {
		t.Errorf("second page %+v", second)
	}
	nowhere, err := page.Encode(apiKeysList, kernel.Cursor[string]{Key: "x", ID: "nope"})
	if err != nil {
		t.Fatal(err)
	}
	for _, cursor := range []string{"junk", nowhere} {
		if rec := w.do(
			t,
			http.MethodGet,
			"/api-keys?cursor="+cursor,
			bearer,
			"",
		); rec.Code != http.StatusBadRequest {
			t.Errorf("cursor %q: %d %s", cursor, rec.Code, rec.Body)
		}
	}

	rec = w.do(t, http.MethodPut, "/api-keys/"+created.Id, bearer, `{"name":"renamed","teams":[]}`)
	if u := decode[api.ApiKey](t, rec); rec.Code != http.StatusOK || u.Name != "renamed" ||
		len(u.Teams) != 0 || u.Description != "CI" {
		t.Errorf("update: %d %+v", rec.Code, u)
	}

	// The created key's token stops when the key is reset.
	user := decode[api.User](t, w.do(t, http.MethodPost, public, "",
		fmt.Sprintf(`{"id":%q,"secret":%q}`, created.Id, *created.Secret)))
	rec = w.do(t, http.MethodPost, "/api-keys/"+created.Id+"/reset", bearer, "")
	reset := decode[api.ApiKey](t, rec)
	if rec.Code != http.StatusOK || reset.Secret == nil || *reset.Secret == *created.Secret {
		t.Fatalf("reset: %d %+v", rec.Code, reset)
	}
	wantUnauthenticated(t, w.do(t, http.MethodGet, "/viewer", "Bearer "+user.Jwt, ""), true)
	wantUnauthenticated(t, w.do(t, http.MethodPost, public, "",
		fmt.Sprintf(`{"id":%q,"secret":%q}`, created.Id, *created.Secret)), false)
	if rec := w.do(
		t,
		http.MethodPost,
		public,
		"",
		fmt.Sprintf(
			`{"id":%q,"secret":%q}`,
			created.Id,
			*reset.Secret,
		),
	); rec.Code != http.StatusOK {
		t.Errorf("the new secret: %d %s", rec.Code, rec.Body)
	}

	rec = w.do(t, http.MethodDelete, "/api-keys/"+created.Id, bearer, "")
	if d := decode[api.ApiKey](t, rec); rec.Code != http.StatusOK || !d.Deleted {
		t.Errorf("delete: %d %+v", rec.Code, d)
	}
	for _, path := range []string{"/api-keys/" + created.Id, "/api-keys/not-a-ulid"} {
		wantNotFound(t, w.do(t, http.MethodGet, path, bearer, ""))
	}

	if rec := w.do(
		t,
		http.MethodPut,
		"/account/api-key-non-admin-managers",
		bearer,
		`{"enable":true}`,
	); rec.Code != http.StatusOK ||
		strings.TrimSpace(rec.Body.String()) != "true" {
		t.Errorf("non-admin managers: %d %s", rec.Code, rec.Body)
	}
}

// TestUnitAPIKeyAccounts checks that a key of another account answers 404
// to every operation on it, as a missing one does.
func TestUnitAPIKeyAccounts(t *testing.T) {
	w := newWorld(t)
	bearer := "Bearer " + w.exchange(t).Jwt
	theirs := kernel.NewULID()
	w.st.keys[theirs] = apikey.Key{
		AccountID: kernel.NewAccountID(),
		ID:        theirs,
		Name:      "theirs",
		Type:      apikey.TypeSecret,
	}
	for _, op := range []struct{ method, path, body string }{
		{http.MethodGet, "/api-keys/" + theirs.String(), ""},
		{http.MethodPut, "/api-keys/" + theirs.String(), `{"name":"mine"}`},
		{http.MethodPost, "/api-keys/" + theirs.String() + "/reset", ""},
		{http.MethodDelete, "/api-keys/" + theirs.String(), ""},
	} {
		wantNotFound(t, w.do(t, op.method, op.path, bearer, op.body))
	}
	if w.st.keys[theirs].Name != "theirs" {
		t.Error("another account's key changed")
	}
	page := decode[api.ApiKeyPage](t, w.do(t, http.MethodGet, "/api-keys", bearer, ""))
	for _, k := range page.Items {
		if k.Id == theirs.String() {
			t.Error("the list shows another account's key")
		}
	}
}

// TestUnitAPIKeyRefusals checks the 422s of input the service refuses,
// the OIDC key's shape, and the store's failure as 503.
func TestUnitAPIKeyRefusals(t *testing.T) {
	w := newWorld(t)
	bearer := "Bearer " + w.exchange(t).Jwt
	for name, body := range map[string]string{
		"no name":     `{"name":"","teams":[]}`,
		"http issuer": `{"name":"o","teams":[],"oidc":{"issuer":"http://idp.test","clientId":"c","subjectExpression":"s"}}`,
	} {
		rec := w.do(t, http.MethodPost, "/api-keys", bearer, body)
		if p := decode[problem.Problem](t, rec); rec.Code != http.StatusUnprocessableEntity ||
			p.Type != problem.ValidationFailed.New("").Type {
			t.Errorf("%s: %d %+v", name, rec.Code, p)
		}
	}
	rec := w.do(t, http.MethodPost, "/api-keys", bearer, `{"name":"o","teams":["ops"],"oidc":{
		"issuer":"https://idp.test","clientId":"c","subjectExpression":"repo:.*",
		"claimMappings":{"entries":[{"name":"cognito:groups","value":"groups"}]}}}`)
	o := decode[api.ApiKey](t, rec)
	if rec.Code != http.StatusCreated || o.Secret != nil || o.Type != api.APIKeyTypeOIDC ||
		o.OidcSettings == nil || o.OidcSettings.Issuer != "https://idp.test" ||
		len(o.OidcSettings.ClaimMapping.Entries) != 1 {
		t.Fatalf("OIDC create: %d %+v", rec.Code, o)
	}
	rec = w.do(t, http.MethodPut, "/api-keys/"+o.Id, bearer,
		`{"oidcClaimMappings":{"entries":[{"name":"roles","value":"groups"}]}}`)
	if u := decode[api.ApiKey](t, rec); rec.Code != http.StatusOK ||
		u.OidcSettings.ClaimMapping.Entries[0].Name != "roles" {
		t.Errorf("claim mapping update: %d %+v", rec.Code, u)
	}
	if rec := w.do(
		t,
		http.MethodPost,
		"/api-keys/"+o.Id+"/reset",
		bearer,
		"",
	); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("reset of an OIDC key: %d %s", rec.Code, rec.Body)
	}
	w.st.keys[w.key] = disabled(w.st.keys[w.key])
	if rec := w.do(
		t,
		http.MethodGet,
		"/api-keys/"+w.key.String(),
		bearer,
		"",
	); rec.Code != http.StatusOK ||
		decode[api.ApiKey](t, rec).DisabledReason == nil {
		t.Errorf("a disabled key: %d %s", rec.Code, rec.Body)
	}
	// The account lets only people manage keys: each change a key makes
	// answers 403, and reading still works.
	w.st.peopleOnly = true
	for _, op := range []struct{ method, path, body string }{
		{http.MethodPost, "/api-keys", `{"name":"x","teams":[]}`},
		{http.MethodPut, "/api-keys/" + o.Id, `{"name":"y"}`},
		{http.MethodDelete, "/api-keys/" + o.Id, ""},
		{http.MethodPut, "/account/api-key-non-admin-managers", `{"enable":false}`},
	} {
		rec := w.do(t, op.method, op.path, bearer, op.body)
		if p := decode[problem.Problem](t, rec); rec.Code != http.StatusForbidden ||
			p.Type != problem.Forbidden.New("").Type {
			t.Errorf("%s %s by a key: %d %+v", op.method, op.path, rec.Code, p)
		}
	}
	if rec := w.do(t, http.MethodGet, "/api-keys", bearer, ""); rec.Code != http.StatusOK {
		t.Errorf("listing by a key: %d %s", rec.Code, rec.Body)
	}
	w.st.peopleOnly = false
	w.st.down = true
	for _, op := range []struct{ method, path, body string }{
		{http.MethodGet, "/api-keys", ""},
		{http.MethodPost, "/api-keys", `{"name":"x","teams":[]}`},
		{http.MethodPut, "/account/api-key-non-admin-managers", `{"enable":false}`},
	} {
		// The session is cached, so the store being down reaches the operation.
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
}

// TestUnitAPIKeysNoPrincipal checks that each operation refuses a request
// that reaches it with no principal.
func TestUnitAPIKeysNoPrincipal(t *testing.T) {
	s := Server{}
	ctx := t.Context()
	for name, call := range map[string]func() error{
		"list": func() error { _, err := s.ApiKeys(ctx, api.ApiKeysRequestObject{}); return err },
		"create": func() error {
			_, err := s.ApiKeyCreate(ctx, api.ApiKeyCreateRequestObject{Body: &api.ApiKeyInput{}})
			return err
		},
		"read": func() error { _, err := s.ApiKey(ctx, api.ApiKeyRequestObject{}); return err },
		"update": func() error {
			_, err := s.ApiKeyUpdate(ctx, api.ApiKeyUpdateRequestObject{Body: &api.ApiKeyUpdateInput{}})
			return err
		},
		"reset":  func() error { _, err := s.ApiKeyReset(ctx, api.ApiKeyResetRequestObject{}); return err },
		"delete": func() error { _, err := s.ApiKeyDelete(ctx, api.ApiKeyDeleteRequestObject{}); return err },
		"managers": func() error {
			_, err := s.ApiKeyEnableNonAdminManagers(ctx, api.ApiKeyEnableNonAdminManagersRequestObject{
				Body: &api.ApiKeyEnableNonAdminManagersJSONRequestBody{},
			})
			return err
		},
	} {
		if pe, ok := errors.AsType[*problem.Error](call()); !ok ||
			pe.Problem.Status != http.StatusUnauthorized {
			t.Errorf("%s without a principal: %v", name, pe)
		}
	}
}

func disabled(k apikey.Key) apikey.Key {
	k.DisabledAt, k.DisabledReason = t0, apikey.DisabledByAdmin
	return k
}

func wantNotFound(t *testing.T, rec interface {
	Result() *http.Response
},
) {
	t.Helper()
	r := rec.Result()
	defer r.Body.Close()
	if r.StatusCode != http.StatusNotFound {
		t.Errorf("%d, want 404", r.StatusCode)
	}
}
