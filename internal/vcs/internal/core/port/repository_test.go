package port

import (
	"errors"
	"testing"

	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

func TestUnitMatchRepositoryKeepsOpaqueIDsAndCurrentCoordinates(t *testing.T) {
	for _, test := range []struct {
		name, expected, actual, namespace, repository string
		refused                                       bool
	}{
		{"unbound legacy source", "", "other", "moved", "other", false},
		{"same identity", "123", "123", "acme", "infra", false},
		{"UUID spellings", "{12345678-1234-4000-8000-123456789abc}", "12345678-1234-4000-8000-123456789ABC", "Acme", "Infra", false},
		{"numeric spellings remain distinct", "00123", "123", "acme", "infra", true},
		{"opaque IDs remain distinct", "ID", "id", "acme", "infra", true},
		{"missing native ID", "123", "", "acme", "infra", true},
		{"unobserved namespace redirect", "123", "123", "other", "infra", true},
		{"unobserved repository redirect", "123", "123", "acme", "other", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := MatchRepository(Repository{Namespace: "acme", Name: "infra", ExpectedID: test.expected},
				vcsapi.Repository{ID: test.actual, Namespace: test.namespace, Name: test.repository})
			if errors.Is(err, ErrRepositoryIdentity) != test.refused || err != nil && !test.refused {
				t.Fatal(err)
			}
		})
	}
}
