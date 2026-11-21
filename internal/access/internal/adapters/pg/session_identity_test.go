package pg_test

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestAccSessionRetainsOriginalAuthenticationFacts(t *testing.T) {
	x := seed(t, pool(t), "original-authentication")
	for _, kind := range []string{"unavailable", "known null", "known empty", "known teams"} {
		t.Run(kind, func(t *testing.T) {
			ss := x.keySession(time.Now().Add(time.Hour))
			ss.Teams = []string{"effective-policy-team"}
			if kind != "unavailable" {
				member, subject := true, "exact-authenticating-subject"
				ss.OriginalMember, ss.IDPSubject = &member, &subject
				ss.OriginalTeamsKnown = true
				ss.IdentityProviderID = kernel.NewULID()
			}
			switch kind {
			case "known empty":
				ss.OriginalTeams = []string{}
			case "known teams":
				ss.OriginalTeams = []string{"original-provider-team"}
			}
			if err := x.s.CreateSession(t.Context(), ss); err != nil {
				t.Fatal(err)
			}
			got, err := x.s.Session(t.Context(), ss.ID)
			if err != nil || got.IdentityProviderID != ss.IdentityProviderID || !reflect.DeepEqual(got.IDPSubject, ss.IDPSubject) ||
				!reflect.DeepEqual(got.OriginalMember, ss.OriginalMember) || got.OriginalTeamsKnown != ss.OriginalTeamsKnown ||
				!reflect.DeepEqual(got.OriginalTeams, ss.OriginalTeams) || !slices.Equal(got.Teams, ss.Teams) {
				t.Fatalf("authentication facts became policy output or lost presence: %+v, %v", got, err)
			}
			if len(got.OriginalTeams) > 0 {
				got.OriginalTeams[0] = "caller-mutated-copy"
				stored, err := x.s.Session(t.Context(), ss.ID)
				if err != nil || !slices.Equal(stored.OriginalTeams, ss.OriginalTeams) {
					t.Fatalf("returned identity facts mutated storage: %+v, %v", stored, err)
				}
			}
		})
	}
}
