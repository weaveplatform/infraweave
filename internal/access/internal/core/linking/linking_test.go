package linking_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/access/internal/core/linking"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/member"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/organization"
	"github.com/weaveplatform/infraweave/internal/access/internal/core/sso"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

var t0 = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

// open is a link of two new members opened at t0.
func open() linking.Link {
	return linking.Link{
		OrganizationID: kernel.NewOrganizationID(), ID: kernel.NewULID(),
		MemberID: kernel.NewMemberID(), OtherMemberID: kernel.NewMemberID(),
		CreatedAt: t0, ExpiresAt: t0.Add(linking.TTL),
	}
}

// TestUnitLinkable refuses a link that could never complete, naming the
// member at fault, and admits two live members that each hold an
// identity, a pending one included.
func TestUnitLinkable(t *testing.T) {
	a := linking.Candidate{ID: kernel.NewMemberID(), Status: member.Active, Identities: 1}
	b := linking.Candidate{ID: kernel.NewMemberID(), Status: member.Pending, Identities: 2}
	if err := linking.Linkable(a, b); err != nil {
		t.Errorf("two live members with identities: %v", err)
	}
	for name, tc := range map[string]struct {
		a, b  linking.Candidate
		field string
	}{
		"one member twice":     {a, a, "otherMemberId"},
		"a deactivated member": {linking.Candidate{ID: a.ID, Status: member.Deactivated, Identities: 1}, b, "memberId"},
		"an other without one": {a, linking.Candidate{ID: b.ID, Status: member.Active}, "otherMemberId"},
		"a deactivated other":  {a, linking.Candidate{ID: b.ID, Status: member.Deactivated, Identities: 1}, "otherMemberId"},
		"a member without one": {linking.Candidate{ID: a.ID, Status: member.Active}, b, "memberId"},
	} {
		var fe *organization.FieldError
		if err := linking.Linkable(tc.a, tc.b); !errors.As(err, &fe) || fe.Field != tc.field ||
			!errors.Is(err, organization.ErrInvalid) {
			t.Errorf("%s = %v, want a field error naming %s", name, err, tc.field)
		}
	}
}

// TestUnitProve proves each member once, in either order, merges on the
// second, and refuses a sign-in the link does not take, leaving it as it
// was.
func TestUnitProve(t *testing.T) {
	l := open()
	if l.Open(t0.Add(linking.TTL)) || !l.Open(t0.Add(linking.TTL-time.Second)) {
		t.Error("a link is open for exactly its TTL")
	}
	first, done, err := linking.Prove(l, l.OtherMemberID, kernel.MemberID{}, t0.Add(time.Minute))
	if err != nil || done || first.OtherMemberSignedAt != t0.Add(time.Minute) || !first.MemberSignedAt.IsZero() {
		t.Fatalf("the other member's sign-in = %+v, %t, %v", first, done, err)
	}
	again, done, err := linking.Prove(first, l.OtherMemberID, kernel.MemberID{}, t0.Add(2*time.Minute))
	if err != nil || done || again.OtherMemberSignedAt != t0.Add(2*time.Minute) {
		t.Errorf("the other member again = %+v, %t, %v", again, done, err)
	}
	for name, tc := range map[string]struct {
		l     linking.Link
		m, by kernel.MemberID
		at    time.Time
		want  error
	}{
		"a third member":     {first, kernel.NewMemberID(), l.OtherMemberID, t0, linking.ErrNotLinked},
		"another browser":    {first, l.MemberID, kernel.NewMemberID(), t0, linking.ErrOtherBrowser},
		"no browser session": {first, l.MemberID, kernel.MemberID{}, t0, linking.ErrOtherBrowser},
		"after the hour":     {first, l.MemberID, l.OtherMemberID, t0.Add(linking.TTL), linking.ErrEnded},
	} {
		got, done, err := linking.Prove(tc.l, tc.m, tc.by, tc.at)
		if !errors.Is(err, tc.want) || done || got != tc.l {
			t.Errorf("%s = %+v, %t, %v, want %v and the link unchanged", name, got, done, err, tc.want)
		}
	}
	merged, done, err := linking.Prove(first, l.MemberID, l.OtherMemberID, t0.Add(59*time.Minute))
	if err != nil || !done || merged.CompletedAt != t0.Add(59*time.Minute) || merged.MemberSignedAt != merged.CompletedAt {
		t.Fatalf("the member's sign-in = %+v, %t, %v", merged, done, err)
	}
	if _, _, err := linking.Prove(merged, l.MemberID, l.MemberID, t0.Add(time.Hour-time.Second)); !errors.Is(err, linking.ErrEnded) {
		t.Errorf("a completed link = %v", err)
	}
	firstMember, _, _ := linking.Prove(l, l.MemberID, kernel.MemberID{}, t0)
	if _, done, err := linking.Prove(firstMember, l.OtherMemberID, l.MemberID, t0); err != nil || !done {
		t.Errorf("the member first, then the other = %t, %v", done, err)
	}
}

// TestUnitPlanUnlink removes every identity of the kind but the session's
// own, and refuses a kind the member holds none of, an unlink of the
// session's own identity alone, and one that leaves no identity.
func TestUnitPlanUnlink(t *testing.T) {
	okta := linking.Federation{ProviderID: kernel.NewULID(), Kind: sso.OIDC, Subject: "a", Login: "nora"}
	nav := linking.Federation{ProviderID: kernel.NewULID(), Kind: sso.OIDC, Subject: "b"}
	saml := linking.Federation{ProviderID: kernel.NewULID(), Kind: sso.SAML, Subject: "c"}
	all := []linking.Federation{okta, nav, saml}
	own := okta
	got, err := linking.PlanUnlink(all, sso.OIDC, &own)
	if err != nil || len(got) != 1 || got[0] != nav {
		t.Errorf("OIDC kept by an OIDC session = %v, %v", got, err)
	}
	if left := linking.Without(all, got); len(left) != 2 || left[0] != okta || left[1] != saml {
		t.Errorf("what remains = %v", left)
	}
	if got, err := linking.PlanUnlink(all, sso.SAML, &own); err != nil || len(got) != 1 || got[0] != saml {
		t.Errorf("SAML by an OIDC session = %v, %v", got, err)
	}
	if got, err := linking.PlanUnlink(all, sso.OIDC, nil); err != nil || len(got) != 2 {
		t.Errorf("OIDC by a personal key = %v, %v", got, err)
	}
	if _, err := linking.PlanUnlink(all, sso.GitHubOAuth, &own); !errors.Is(err, linking.ErrNoIdentity) {
		t.Errorf("a kind the member holds none of = %v", err)
	}
	var fe *organization.FieldError
	if _, err := linking.PlanUnlink([]linking.Federation{okta, saml}, sso.OIDC, &own); !errors.As(err, &fe) ||
		fe.Field != "provider" {
		t.Errorf("the session's own identity alone = %v", err)
	}
	if _, err := linking.PlanUnlink([]linking.Federation{okta, nav}, sso.OIDC, nil); !errors.As(err, &fe) {
		t.Errorf("every identity = %v", err)
	}
	var body struct {
		Removed []map[string]string `json:"removed"`
	}
	if err := json.Unmarshal(linking.UnlinkBody([]linking.Federation{okta}), &body); err != nil ||
		len(body.Removed) != 1 || body.Removed[0]["externalUsername"] != "nora" ||
		body.Removed[0]["provider"] != "OIDC" || body.Removed[0]["providerId"] != okta.ProviderID.String() {
		t.Errorf("the unlink's trail body = %+v, %v", body, err)
	}
}

// TestUnitMergeEntry is the merge's trail entry: the member that remains
// acts on itself, naming the link and the member merged into it.
func TestUnitMergeEntry(t *testing.T) {
	l := open()
	e := linking.MergeEntry(l, "192.0.2.1", t0)
	var body map[string]string
	if err := json.Unmarshal(e.Body, &body); err != nil || e.Action != linking.ActionMerge ||
		e.ActorKind != kernel.PrincipalUser || e.ActorID != l.MemberID.String() ||
		e.AffectedResource.ID != l.MemberID.String() || body["otherMemberId"] != l.OtherMemberID.String() ||
		body["linkId"] != l.ID.String() || e.RemoteIP != "192.0.2.1" || e.CreatedAt != t0 {
		t.Errorf("entry = %+v, body %v, %v", e, body, err)
	}
}
