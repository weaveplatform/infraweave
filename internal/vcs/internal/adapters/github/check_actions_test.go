package github_test

import (
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestUnitCheckActionCollaboratorPermission(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	for _, tc := range []struct {
		name, permission string
		want             bool
	}{
		{"write", "write", true}, {"admin", "admin", true}, {"read", "read", false}, {"triage", "read", false}, {"none", "none", false}, {"unknown", "custom-role", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l.fake.SetCollaboratorPermission("acme", "infra", "developer", tc.permission)
			got, err := l.gh.CanWrite(t.Context(), l.repo.Repo, "developer")
			if err != nil || got != tc.want {
				t.Fatal("native collaborator permission", got, err)
			}
			if len(l.fake.CheckWrites("acme", "infra")) != 0 {
				t.Fatal("permission read mutated checks")
			}
		})
	}
	if ok, err := l.gh.CanWrite(t.Context(), l.repo.Repo, ""); ok || !errors.Is(err, port.ErrCheckActionTarget) {
		t.Fatal("empty sender", ok, err)
	}
	l.in.Credential = []byte("invalid private key")
	if ok, err := l.gh.CanWrite(t.Context(), l.repo.Repo, "developer"); ok || !errors.Is(err, port.ErrCredential) {
		t.Fatal("provider denial authorized sender", ok, err)
	}
}
