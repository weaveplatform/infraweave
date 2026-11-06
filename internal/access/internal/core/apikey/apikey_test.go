package apikey

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestUnitSecret(t *testing.T) {
	s := NewSecret()
	if !strings.HasPrefix(s, Prefix) || len(s) != len(Prefix)+43 || s == NewSecret() {
		t.Fatalf("NewSecret = %q", s)
	}
	h, ok := Hash(s)
	if !ok || len(h) != 32 {
		t.Fatalf("Hash = %x, %v", h, ok)
	}
	k := Key{Type: TypeSecret, SecretHash: h}
	if !k.Matches(s) {
		t.Error("a key does not match its own secret")
	}
	for _, wrong := range []string{
		NewSecret(),
		"",
		strings.TrimPrefix(s, Prefix),
		"iwo_" + strings.TrimPrefix(s, Prefix),
		s[:len(s)-1],
		s + "A",
		Prefix + strings.Repeat("!", 43),
	} {
		if k.Matches(wrong) {
			t.Errorf("Matches(%q)", wrong)
		}
	}
	if (Key{Type: TypeOIDC, SecretHash: h}).Matches(s) {
		t.Error("an OIDC key matched a secret")
	}
}

func TestUnitUsable(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		k    Key
		want bool
	}{
		"plain":       {Key{}, true},
		"not expired": {Key{ExpiresAt: now.Add(time.Second)}, true},
		"expired":     {Key{ExpiresAt: now}, false},
		"disabled":    {Key{DisabledAt: now}, false},
		"deleted":     {Key{DeletedAt: now}, false},
	} {
		if got := tc.k.Usable(now); got != tc.want {
			t.Errorf("%s: Usable = %v", name, got)
		}
	}
}

// TestUnitCompileSubject checks that a subject expression matches whole
// subjects only, so an unanchored pattern cannot be bypassed by a prefix
// or a suffix.
func TestUnitCompileSubject(t *testing.T) {
	re, err := CompileSubject("repo:org/app:ref:refs/heads/main|repo:org/app:environment:prod")
	if err != nil {
		t.Fatal(err)
	}
	for sub, want := range map[string]bool{
		"repo:org/app:ref:refs/heads/main":          true,
		"repo:org/app:environment:prod":             true,
		"x-repo:org/app:ref:refs/heads/main":        false,
		"repo:org/app:ref:refs/heads/main-evil":     false,
		"repo:org/app:environment:prod\nrepo:other": false,
	} {
		if re.MatchString(sub) != want {
			t.Errorf("%q: matched %v", sub, !want)
		}
	}
	if _, err := CompileSubject("repo:("); err == nil {
		t.Error("an invalid expression compiled")
	}
}

// TestUnitTeams checks that an OIDC key's teams are its own and those of
// the groups claim after the mapping, each once, from a string or a list.
func TestUnitTeams(t *testing.T) {
	o := OIDC{ClaimMapping: []Mapping{{Name: "cognito:groups", Value: TeamsClaim}}}
	for name, tc := range map[string]struct {
		claims map[string]any
		want   []string
	}{
		"mapped list":   {map[string]any{"cognito:groups": []any{"ops", "dev", 7, ""}}, []string{"ops", "dev"}},
		"mapped string": {map[string]any{"cognito:groups": "dev"}, []string{"ops", "dev"}},
		"groups as is":  {map[string]any{"groups": []any{"qa"}}, []string{"ops", "qa"}},
		"none":          {map[string]any{"sub": "x"}, []string{"ops"}},
	} {
		if got := o.Teams([]string{"ops"}, tc.claims); !slices.Equal(got, tc.want) {
			t.Errorf("%s: Teams = %v, want %v", name, got, tc.want)
		}
	}
}
