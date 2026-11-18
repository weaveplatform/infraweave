package port

import (
	"strings"
	"testing"
)

func TestUnitCommitIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, hash string
		valid      bool
	}{
		{"SHA1", strings.Repeat("a", 40), true}, {"SHA256", strings.Repeat("b", 64), true}, {"empty", "", false}, {"ref", "refs/heads/main", false}, {"short", "a", false}, {"zero SHA1", strings.Repeat("0", 40), false}, {"zero SHA256", strings.Repeat("0", 64), false}, {"nonhex", strings.Repeat("z", 40), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidCommit(tc.hash); got != tc.valid {
				t.Fatal(got, tc.valid)
			}
		})
	}
}
