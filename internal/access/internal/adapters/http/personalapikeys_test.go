package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/apikey"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/session"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

func (s *store) CreatePersonalAPIKey(_ context.Context, k apikey.Key) (apikey.Key, error) {
	if err := s.err(); err != nil {
		return apikey.Key{}, err
	}
	if s.personalOff {
		return apikey.Key{}, apikey.ErrPersonalOff
	}
	o, ok := s.owners[k.Owner]
	if !ok {
		return apikey.Key{}, apikey.ErrNoOwner
	}
	k.OwnerLogin = o.Login
	s.keys[k.ID] = k
	return k, nil
}

func (s *store) AccountPersonalAPIKeys(
	ctx context.Context,
	acct kernel.AccountID,
	after kernel.ULID,
	limit int,
) ([]apikey.Key, error) {
	all, err := s.AccountAPIKeys(ctx, acct, after, len(s.keys))
	var out []apikey.Key
	for _, k := range all {
		if k.Personal() {
			out = append(out, k)
		}
	}
	return out[:min(len(out), limit)], err
}

func (s *store) CountPersonalAPIKeys(ctx context.Context, acct kernel.AccountID) (int, error) {
	keys, err := s.AccountPersonalAPIKeys(ctx, acct, kernel.ULID{}, len(s.keys))
	n := 0
	for _, k := range keys {
		if k.DisabledAt.IsZero() {
			n++
		}
	}
	return n, err
}

func (s *store) DisablePersonalAPIKey(
	ctx context.Context,
	acct kernel.AccountID,
	id kernel.ULID,
	reason string,
) (apikey.Key, []kernel.ULID, error) {
	if k, ok := s.keys[id]; ok && !k.Personal() {
		return apikey.Key{}, nil, port.ErrNotFound
	}
	return s.DisableAPIKey(ctx, acct, id, reason)
}

func (s *store) PersonalCreatable(
	_ context.Context,
	_ kernel.AccountID,
	member kernel.MemberID,
) error {
	if _, ok := s.owners[member]; !ok {
		return apikey.ErrNoOwner
	}
	if s.personalOff {
		return apikey.ErrPersonalOff
	}
	return s.err()
}

func (s *store) KeysOf(
	ctx context.Context,
	acct kernel.AccountID,
	login string,
	member kernel.MemberID,
) ([]kernel.ULID, []kernel.ULID, error) {
	keys, err := s.AccountAPIKeys(ctx, acct, kernel.ULID{}, len(s.keys))
	var created, owned []kernel.ULID
	for _, k := range keys {
		if k.Creator == login {
			created = append(created, k.ID)
		}
		if k.Personal() && k.Owner == member {
			owned = append(owned, k.ID)
		}
	}
	return created, owned, err
}

func (s *store) KeyOwner(
	_ context.Context,
	_ kernel.AccountID,
	member kernel.MemberID,
) (apikey.Owner, error) {
	o, ok := s.owners[member]
	if !ok {
		return apikey.Owner{}, port.ErrNotFound
	}
	return o, s.err()
}

// memberToken is a bearer token of a member's CLI session in acct, which
// the account admits as an owner of personal keys.
func (w *world) memberToken(acct kernel.AccountID) (kernel.MemberID, string) {
	ss := session.Session{
		ID: kernel.NewULID(), OrganizationID: kernel.NewOrganizationID(), AccountID: acct,
		Channel: session.ChannelCLI, ActorKind: kernel.PrincipalUser, ActorID: kernel.NewULID(),
		MemberID: kernel.NewMemberID(), Login: "alice", Name: "Alice", CreatedAt: t0,
		ExpiresAt: t0.Add(time.Hour),
	}
	w.st.sessions[ss.ID] = ss
	if w.st.owners == nil {
		w.st.owners = map[kernel.MemberID]apikey.Owner{}
	}
	w.st.owners[ss.MemberID] = apikey.Owner{Login: "alice", Name: "Alice", Teams: []string{"dev"}}
	return ss.MemberID, fmt.Sprintf("Bearer user %s %s", ss.ID, audience)
}

// TestUnitPersonalAPIKeys runs the Personal API Keys operations through
// the router: a member creates a key, whose token acts as them; an admin
// lists, counts and disables it; and a key of the account's own is not
// personal.
func TestUnitPersonalAPIKeys(t *testing.T) {
	w := newWorld(t)
	bearer := "Bearer " + w.exchange(t).Jwt
	_, alice := w.memberToken(w.acct)
	if v := decode[api.User](
		t,
		w.do(t, http.MethodGet, "/viewer", alice, ""),
	); !v.CanCreatePersonalApiKey ||
		len(v.OwnedApiKeys) != 0 ||
		len(v.ApiKeys) != 0 {
		t.Errorf("alice's viewer before her key: %+v", v)
	}

	rec := w.do(t, http.MethodPost, "/personal-api-keys", alice,
		`{"name":"laptop","description":"mine","expiresAt":2000000000}`)
	k := decode[api.ApiKey](t, rec)
	if rec.Code != http.StatusCreated || !k.IsPersonal || k.Owner == nil || *k.Owner != "alice" ||
		k.Secret == nil || k.Name != "laptop" || k.Description != "mine" ||
		k.ExpiresAt == nil || *k.ExpiresAt != 2000000000 || len(k.Teams) != 0 {
		t.Fatalf("create: %d %+v", rec.Code, k)
	}
	rec = w.do(t, http.MethodPost, public, "",
		fmt.Sprintf(`{"id":%q,"secret":%q}`, k.Id, *k.Secret))
	if u := decode[api.User](t, rec); rec.Code != http.StatusOK || u.Name != "Alice" {
		t.Errorf("the personal key's exchange: %d %+v", rec.Code, u)
	}

	mine := []api.ApiKeyRef{{Id: k.Id}}
	if v := decode[api.User](
		t,
		w.do(t, http.MethodGet, "/viewer", alice, ""),
	); !v.CanCreatePersonalApiKey ||
		!slices.Equal(v.OwnedApiKeys, mine) ||
		!slices.Equal(v.ApiKeys, mine) {
		t.Errorf("alice's viewer: %+v", v)
	}
	if v := decode[api.User](
		t,
		w.do(t, http.MethodGet, "/viewer", bearer, ""),
	); v.CanCreatePersonalApiKey ||
		len(v.OwnedApiKeys) != 0 {
		t.Errorf("the API key's viewer: %+v", v)
	}

	list := decode[api.ApiKeyPage](t, w.do(t, http.MethodGet, "/personal-api-keys", bearer, ""))
	if len(list.Items) != 1 || list.Items[0].Id != k.Id || !list.Items[0].IsPersonal {
		t.Errorf("list: %+v", list)
	}
	if rec := w.do(t, http.MethodGet, "/personal-api-keys-count", bearer, ""); rec.Code !=
		http.StatusOK || strings.TrimSpace(rec.Body.String()) != "1" {
		t.Errorf("count: %d %s", rec.Code, rec.Body)
	}

	rec = w.do(t, http.MethodPost, "/personal-api-keys/"+k.Id+"/disable", bearer, "")
	if d := decode[api.ApiKey](t, rec); rec.Code != http.StatusOK || !d.Disabled ||
		d.DisabledReason == nil || *d.DisabledReason != api.ApiKeyDisabledReason("ADMIN") {
		t.Errorf("disable: %d %+v", rec.Code, d)
	}
	if rec := w.do(t, http.MethodGet, "/personal-api-keys-count", bearer, ""); strings.TrimSpace(
		rec.Body.String()) != "0" {
		t.Errorf("count after disabling: %s", rec.Body)
	}
	for _, id := range []string{w.key.String(), "not-a-ulid"} {
		wantNotFound(t, w.do(t, http.MethodPost, "/personal-api-keys/"+id+"/disable", bearer, ""))
	}
}

// TestUnitPersonalAPIKeyRefusals checks that only a member creates a
// personal key, and only while the account lets them, and the store's
// failures.
func TestUnitPersonalAPIKeyRefusals(t *testing.T) {
	w := newWorld(t)
	bearer := "Bearer " + w.exchange(t).Jwt
	_, alice := w.memberToken(w.acct)
	const body = `{"name":"laptop"}`

	if rec := w.do(t, http.MethodPost, "/personal-api-keys", bearer, body); rec.Code !=
		http.StatusForbidden {
		t.Errorf("an API key's create: %d %s", rec.Code, rec.Body)
	}
	w.st.personalOff = true
	if rec := w.do(t, http.MethodPost, "/personal-api-keys", alice, body); rec.Code !=
		http.StatusForbidden {
		t.Errorf("with personal keys off: %d %s", rec.Code, rec.Body)
	}
	if v := decode[api.User](
		t,
		w.do(t, http.MethodGet, "/viewer", alice, ""),
	); v.CanCreatePersonalApiKey {
		t.Errorf("alice's viewer with personal keys off: %+v", v)
	}
	w.st.personalOff = false
	if rec := w.do(t, http.MethodPost, "/personal-api-keys", alice, `{"name":""}`); rec.Code !=
		http.StatusUnprocessableEntity {
		t.Errorf("no name: %d %s", rec.Code, rec.Body)
	}
	w.st.down = true
	for _, path := range []string{"/personal-api-keys", "/personal-api-keys-count", "/viewer"} {
		if rec := w.do(t, http.MethodGet, path, alice, ""); rec.Code !=
			http.StatusServiceUnavailable {
			t.Errorf("%s with the store down: %d %s", path, rec.Code, rec.Body)
		}
	}
}

// TestUnitPersonalAPIKeysNoPrincipal checks that each operation refuses a
// request that reaches it with no principal.
func TestUnitPersonalAPIKeysNoPrincipal(t *testing.T) {
	s := Server{}
	ctx := t.Context()
	for name, call := range map[string]func() error{
		"list": func() error {
			_, err := s.PersonalApiKeys(ctx, api.PersonalApiKeysRequestObject{})
			return err
		},
		"create": func() error {
			_, err := s.PersonalApiKeyCreate(ctx, api.PersonalApiKeyCreateRequestObject{})
			return err
		},
		"count": func() error {
			_, err := s.PersonalApiKeysCount(ctx, api.PersonalApiKeysCountRequestObject{})
			return err
		},
		"disable": func() error {
			_, err := s.PersonalApiKeyDisable(ctx, api.PersonalApiKeyDisableRequestObject{})
			return err
		},
	} {
		if pe, ok := errors.AsType[*problem.Error](call()); !ok ||
			pe.Problem.Status != http.StatusUnauthorized {
			t.Errorf("%s without a principal: %v", name, pe)
		}
	}
}
