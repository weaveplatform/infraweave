package webhookauth_test

import (
	"crypto/fips140"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/webhookauth"
)

func TestUnitSignature(t *testing.T) {
	body := []byte("Hello, World!")
	current, previous := []byte("current-bitbucket-hook-secret-32-bytes"), []byte("previous-bitbucket-hook-secret-32-bytes")
	sign := func(key []byte) string {
		m := hmac.New(sha256.New, key)
		_, _ = m.Write(body)
		return "sha256=" + hex.EncodeToString(m.Sum(nil))
	}
	for _, tc := range []struct {
		name   string
		values []string
		keys   [][]byte
		want   string
	}{
		{name: "current", values: []string{sign(current)}, keys: [][]byte{current, previous}, want: sign(current)},
		{name: "previous", values: []string{sign(previous)}, keys: [][]byte{current, previous}, want: sign(previous)},
		{name: "missing", keys: [][]byte{current}},
		{name: "wrong", values: []string{sign(previous)}, keys: [][]byte{current}},
		{name: "wrong prefix", values: []string{"SHA256=" + strings.TrimPrefix(sign(current), "sha256=")}, keys: [][]byte{current}},
		{name: "SHA1", values: []string{"sha1=" + strings.Repeat("a", 40)}, keys: [][]byte{current}},
		{name: "short header", values: []string{"sha256=a"}, keys: [][]byte{current}},
		{name: "not hex", values: []string{"sha256=" + strings.Repeat("z", 64)}, keys: [][]byte{current}},
		{name: "duplicate header", values: []string{sign(current), sign(current)}, keys: [][]byte{current}},
		{name: "no keys", values: []string{sign(current)}},
		{name: "empty key", values: []string{sign(current)}, keys: [][]byte{nil}},
		{name: "short before strong", values: []string{sign(current)}, keys: [][]byte{[]byte("hook"), current}, want: sign(current)},
		{name: "short after strong", values: []string{sign(current)}, keys: [][]byte{current, []byte("hook")}, want: sign(current)},
		{name: "original uppercase hex", values: []string{"sha256=" + strings.ToUpper(strings.TrimPrefix(sign(current), "sha256="))}, keys: [][]byte{current}, want: "sha256=" + strings.ToUpper(strings.TrimPrefix(sign(current), "sha256="))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := webhookauth.Verify("X-Hub-Signature", http.Header{"X-Hub-Signature": tc.values}, body, tc.keys)
			if tc.want != "" {
				if err != nil || string(got) != tc.want {
					t.Fatalf("signature = %q, %v", got, err)
				}
			} else if !errors.Is(err, webhookauth.ErrSignature) || got != nil {
				t.Fatalf("refusal = %q, %v", got, err)
			}
		})
	}
}

func TestUnitShortKey(t *testing.T) {
	const signature = "sha256=310ffbc4b633f00b468311d85b2223a88b30547e5ddf9ab24dd8404b52a39702"
	got, err := webhookauth.Verify("X-Hub-Signature", http.Header{"X-Hub-Signature": {signature}}, []byte("Hello, World!"), [][]byte{[]byte("hook")})
	if fips140.Enforced() {
		if !errors.Is(err, webhookauth.ErrSignature) || got != nil {
			t.Fatalf("FIPS weak key = %q, %v", got, err)
		}
	} else if err != nil || string(got) != signature {
		t.Fatalf("ordinary HMAC = %q, %v", got, err)
	}
}
