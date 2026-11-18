package lifecycle

import (
	"strings"
	"testing"
	"time"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

func TestUnitReviewOwnerCaseAliasRetainsKnownUnavailability(t *testing.T) {
	for _, action := range []string{"suspend", "deleted"} {
		t.Run(action, func(t *testing.T) {
			w := newWorld()
			owner := vcsapi.Owner{ID: "9", Login: "Acme"}
			repo := vcsapi.Repository{ID: "7", OwnerID: owner.ID, Namespace: owner.Login, Name: "infra"}
			created := vcsapi.Installation{ID: "1", Owner: owner, Action: "created", Repositories: []vcsapi.Repository{repo}}
			if err := w.apply(t, vcsapi.Event{Installation: &created}); err != nil {
				t.Fatal(err)
			}
			w.now = w.now.Add(time.Second)
			changed := vcsapi.Installation{ID: "1", Owner: owner, Action: action}
			if err := w.apply(t, vcsapi.Event{Installation: &changed}); err != nil {
				t.Fatal(err)
			}
			if err := w.s.Check(t.Context(), w.acct, w.id, owner.Login, repo.Name); err == nil {
				t.Fatal("fixture did not establish native installation unavailability")
			}
			if err := w.s.Check(t.Context(), w.acct, w.id, strings.ToUpper(owner.Login), repo.Name); err == nil {
				t.Fatal("case-insensitive GitHub owner alias bypassed known installation unavailability")
			}
		})
	}
}
