package kernel

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestUnitTenancyIDs(t *testing.T) {
	org, account, member := NewOrganizationID(), NewAccountID(), NewMemberID()
	for name, tc := range map[string]struct {
		id    ULID
		parse func(string) (ULID, error)
	}{
		"organization": {org.ULID, func(s string) (ULID, error) {
			v, err := ParseOrganizationID(s)
			return v.ULID, err
		}},
		"account": {account.ULID, func(s string) (ULID, error) {
			v, err := ParseAccountID(s)
			return v.ULID, err
		}},
		"member": {member.ULID, func(s string) (ULID, error) {
			v, err := ParseMemberID(s)
			return v.ULID, err
		}},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.id.IsZero() {
				t.Fatal("a new ID is zero")
			}
			if got, err := tc.parse(tc.id.String()); err != nil || got != tc.id {
				t.Errorf("parse(%s) = %v, %v", tc.id, got, err)
			}
			if _, err := tc.parse("not-an-id"); !errors.Is(err, ErrULID) {
				t.Errorf("parse(not-an-id) = %v, want ErrULID", err)
			}
		})
	}

	// The embedded ULID's methods carry over: the wire form in JSON and the
	// uuid column through database/sql.
	b, err := json.Marshal(struct {
		ID AccountID `json:"id"`
	}{account})
	if err != nil || string(b) != `{"id":"`+account.String()+`"}` {
		t.Errorf("json = %s, %v", b, err)
	}
	var back struct {
		ID AccountID `json:"id"`
	}
	if err := json.Unmarshal(b, &back); err != nil || back.ID != account {
		t.Errorf("json round trip = %v, %v", back.ID, err)
	}
	v, err := org.Value()
	if err != nil {
		t.Fatal(err)
	}
	var scanned OrganizationID
	if err := scanned.Scan(v); err != nil || scanned != org {
		t.Errorf("Scan(Value()) = %v, %v", scanned, err)
	}
}

func TestUnitPrincipalKinds(t *testing.T) {
	want := []string{
		"user",
		"api_key",
		"run",
		"organization",
		"break_glass",
		"operator",
		"worker",
		"runner",
		"anonymous",
	}
	if len(PrincipalKinds) != len(want) {
		t.Fatalf("%d kinds, want %d", len(PrincipalKinds), len(want))
	}
	for i, s := range want {
		k, err := ParsePrincipalKind(s)
		if err != nil || k != PrincipalKinds[i] {
			t.Errorf("ParsePrincipalKind(%q) = %q, %v", s, k, err)
		}
	}
	for _, s := range []string{"", "USER", "apikey", "root"} {
		if _, err := ParsePrincipalKind(s); !errors.Is(err, ErrPrincipalKind) {
			t.Errorf("ParsePrincipalKind(%q) = %v, want ErrPrincipalKind", s, err)
		}
	}
}

func TestUnitPrincipalCaller(t *testing.T) {
	key := NewULID()
	p := Principal{Kind: PrincipalAPIKey, ActorID: key, AccountID: NewAccountID()}
	if got, want := p.Caller(), "api_key:"+key.String(); got != want {
		t.Errorf("Caller() = %q, want %q", got, want)
	}
	// Two sessions of one key are one caller; another key is another.
	same := Principal{Kind: PrincipalAPIKey, ActorID: key, AccountID: p.AccountID}
	other := Principal{Kind: PrincipalAPIKey, ActorID: NewULID(), AccountID: p.AccountID}
	if same.Caller() != p.Caller() || other.Caller() == p.Caller() {
		t.Error("the caller key does not follow the actor")
	}
}

func TestUnitPrincipalContext(t *testing.T) {
	if _, ok := PrincipalFrom(context.Background()); ok {
		t.Error("an empty context carries a principal")
	}
	p := Principal{Kind: PrincipalAPIKey, ActorID: NewULID(), Teams: []string{"ops"}}
	got, ok := PrincipalFrom(WithPrincipal(context.Background(), p))
	if !ok || got.ActorID != p.ActorID || got.Teams[0] != "ops" {
		t.Errorf("PrincipalFrom = %+v, %v", got, ok)
	}
}

func TestUnitPrincipalHuman(t *testing.T) {
	for _, k := range PrincipalKinds {
		want := k == PrincipalUser || k == PrincipalBreakGlass
		if got := (Principal{Kind: k}).Human(); got != want {
			t.Errorf("%s: Human() = %v, want %v", k, got, want)
		}
	}
}
