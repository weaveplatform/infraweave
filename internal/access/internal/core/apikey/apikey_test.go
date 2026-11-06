package apikey

import (
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
