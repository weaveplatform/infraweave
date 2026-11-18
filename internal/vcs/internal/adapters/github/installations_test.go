package github_test

import "testing"

func TestUnitInstallationAvailability(t *testing.T) {
	l := newLab(t)
	first, err := l.gh.ReadInstallations(t.Context(), l.in)
	if err != nil || len(first) != 1 || first[0].Owner.ID == "" || first[0].Owner.Login != "acme" || first[0].State != "active" {
		t.Fatal(first, err)
	}
	// The App list and repository identity agree on stable owner identity.
	resolved, err := l.gh.ResolveRepositoryRef(t.Context(), l.repo.Repo, "main")
	if err != nil || resolved.Repository.OwnerID != first[0].Owner.ID {
		t.Fatal(resolved, err)
	}
	id := l.fake.Install(l.app.ID, "another", "Organization")
	if err := l.fake.SetInstallationState(id, "suspended"); err != nil {
		t.Fatal(err)
	}
	before := len(l.fake.TokenRequests())
	list, err := l.gh.ReadInstallations(t.Context(), l.in)
	if err != nil || len(list) != 2 || list[1].State != "suspended" || list[1].Owner.Login != "another" {
		t.Fatal(list, err)
	}
	if len(l.fake.TokenRequests()) != before {
		t.Fatal("installation refresh minted a token")
	}
	if err := l.fake.SetInstallationState(id, "deleted"); err != nil {
		t.Fatal(err)
	}
	list, err = l.gh.ReadInstallations(t.Context(), l.in)
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
}
