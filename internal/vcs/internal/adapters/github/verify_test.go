package github_test

import (
	"crypto/fips140"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/github"
)

// Configuration can contain an older, short secret. Enforced FIPS must
// refuse it without panicking; other modes preserve GitHub's HMAC semantics.
func TestUnitGitHubShortSignature(t *testing.T) {
	const signature = "sha256=310ffbc4b633f00b468311d85b2223a88b30547e5ddf9ab24dd8404b52a39702"
	got, err := github.New(github.Options{}).Verify(
		http.Header{"X-Hub-Signature-256": {signature}}, []byte("Hello, World!"), [][]byte{[]byte("hook")},
	)
	if fips140.Enforced() {
		if !errors.Is(err, github.ErrSignature) || got != nil {
			t.Fatalf("weak FIPS key = %q, %v", got, err)
		}
	} else if err != nil || string(got) != signature {
		t.Fatalf("ordinary GitHub HMAC = %q, %v", got, err)
	}
}

// GitHub's documented validation example uses the exact body bytes. These
// independent fixtures use longer keys so they also run in certified FIPS.
// https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries
func TestUnitGitHubSignature(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main","sender":{"login":"octocat"}}`)
	current := []byte("current-webhook-secret-at-least-32-bytes")
	previous := []byte("previous-webhook-secret-at-least-32-bytes")
	sign := func(key []byte) string {
		m := hmac.New(sha256.New, key)
		_, _ = m.Write(body)
		return "sha256=" + hex.EncodeToString(m.Sum(nil))
	}
	for _, tc := range []struct {
		name   string
		header http.Header
		body   []byte
		keys   [][]byte
		want   string
	}{
		{name: "current", header: http.Header{"X-Hub-Signature-256": {sign(current)}}, body: body, keys: [][]byte{current, previous}, want: sign(current)},
		{name: "previous", header: http.Header{"X-Hub-Signature-256": {sign(previous)}}, body: body, keys: [][]byte{current, previous}, want: sign(previous)},
		{name: "short candidate before strong", header: http.Header{"X-Hub-Signature-256": {sign(current)}}, body: body, keys: [][]byte{[]byte("hook"), current}, want: sign(current)},
		{name: "short candidate after strong", header: http.Header{"X-Hub-Signature-256": {sign(current)}}, body: body, keys: [][]byte{current, []byte("hook")}, want: sign(current)},
		{name: "missing", body: body, keys: [][]byte{current}},
		{name: "wrong", header: http.Header{"X-Hub-Signature-256": {sign(previous)}}, body: body, keys: [][]byte{current}},
		{name: "changed body", header: http.Header{"X-Hub-Signature-256": {sign(current)}}, body: append([]byte(" "), body...), keys: [][]byte{current}},
		{name: "SHA1 only", header: http.Header{"X-Hub-Signature": {"sha1=" + strings.Repeat("a", 40)}}, body: body, keys: [][]byte{current}},
		{name: "wrong prefix", header: http.Header{"X-Hub-Signature-256": {"SHA256=" + strings.TrimPrefix(sign(current), "sha256=")}}, body: body, keys: [][]byte{current}},
		{name: "short", header: http.Header{"X-Hub-Signature-256": {"sha256=a"}}, body: body, keys: [][]byte{current}},
		{name: "nonhex", header: http.Header{"X-Hub-Signature-256": {"sha256=" + strings.Repeat("z", 64)}}, body: body, keys: [][]byte{current}},
		{name: "duplicate header", header: http.Header{"X-Hub-Signature-256": {sign(current), sign(current)}}, body: body, keys: [][]byte{current}},
		{name: "no keys", header: http.Header{"X-Hub-Signature-256": {sign(current)}}, body: body},
		{name: "empty key", header: http.Header{"X-Hub-Signature-256": {sign(current)}}, body: body, keys: [][]byte{nil}},
		{name: "exact uppercase hex retained", header: http.Header{"X-Hub-Signature-256": {"sha256=" + strings.ToUpper(strings.TrimPrefix(sign(current), "sha256="))}}, body: body, keys: [][]byte{current}, want: "sha256=" + strings.ToUpper(strings.TrimPrefix(sign(current), "sha256="))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := github.New(github.Options{})
			got, err := p.Verify(tc.header, tc.body, tc.keys)
			if tc.want != "" {
				if err != nil || string(got) != tc.want {
					t.Fatalf("signature = %q, %v", got, err)
				}
			} else if !errors.Is(err, github.ErrSignature) || got != nil {
				t.Fatalf("refused signature = %q, %v", got, err)
			}
		})
	}
}
