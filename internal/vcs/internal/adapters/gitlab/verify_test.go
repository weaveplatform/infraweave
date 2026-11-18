package gitlab_test

import (
	"bytes"
	"crypto/fips140"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/gitlab"
)

func TestUnitGitLabToken(t *testing.T) {
	current := []byte("current-gitlab-webhook-secret-32-bytes")
	previous := []byte("previous-gitlab-webhook-secret-32-bytes")
	body := []byte(`{"object_kind":"push","project":{"id":15,"path_with_namespace":"mike/diaspora"}}`)
	provider := gitlab.New(gitlab.Options{})
	for _, tc := range []struct {
		name    string
		token   []string
		secrets [][]byte
		matched []byte
	}{
		{name: "current", token: []string{string(current)}, secrets: [][]byte{current, previous}, matched: current},
		{name: "previous", token: []string{string(previous)}, secrets: [][]byte{current, previous}, matched: previous},
		{name: "current after previous", token: []string{string(current)}, secrets: [][]byte{previous, current}, matched: current},
		{name: "missing", secrets: [][]byte{current, previous}},
		{name: "wrong", token: []string{"wrong"}, secrets: [][]byte{current, previous}},
		{name: "expired previous", token: []string{string(previous)}, secrets: [][]byte{current}},
		{name: "empty token and secret", token: []string{""}, secrets: [][]byte{nil, {}}},
		{name: "no allowed secrets", token: []string{string(current)}},
		{name: "duplicate token", token: []string{string(current), string(current)}, secrets: [][]byte{current}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			headers := http.Header{}
			for _, token := range tc.token {
				headers.Add("X-Gitlab-Token", token)
			}
			signature, err := provider.Verify(headers, body, tc.secrets)
			if tc.matched == nil {
				if err == nil || signature != nil {
					t.Fatal("unverified token returned a retained signature")
				}
				return
			}
			mac := hmac.New(sha256.New, tc.matched)
			_, _ = mac.Write(body)
			want := []byte(hex.EncodeToString(mac.Sum(nil)))
			if err != nil || !hmac.Equal(signature, want) || bytes.Equal(signature, tc.matched) {
				t.Fatal("verification did not retain the matched secret's body digest", err)
			}
			changed, err := provider.Verify(headers, append(bytes.Clone(body), '\n'), tc.secrets)
			if err != nil || hmac.Equal(signature, changed) {
				t.Fatal("stored digest did not bind the exact body bytes", err)
			}
		})
	}
	headers := http.Header{}
	headers.Set("X-Gitlab-Event-UUID", "gitlab-delivery-id")
	if provider.DeliveryID(headers, body) != "gitlab-delivery-id" || provider.DeliveryID(nil, body) != "" {
		t.Fatal("delivery identity did not preserve the provider UUID")
	}
}

func TestUnitGitLabShortTokenPolicy(t *testing.T) {
	provider := gitlab.New(gitlab.Options{})
	strong := []byte("current-gitlab-webhook-secret-32-bytes")
	body := []byte(`{"object_kind":"push"}`)
	for _, key := range []string{"hook", strings.Repeat("x", 13), strings.Repeat("x", 14)} {
		t.Run(fmt.Sprint(len(key)), func(t *testing.T) {
			headers := http.Header{}
			headers.Set("X-Gitlab-Token", key)
			signature, err := provider.Verify(headers, body, [][]byte{strong, []byte(key)})
			if fips140.Enforced() && len(key) < 14 {
				if err == nil || signature != nil {
					t.Fatal("FIPS-disallowed HMAC key was not refused")
				}
				return
			}
			mac := hmac.New(sha256.New, []byte(key))
			_, _ = mac.Write(body)
			if err != nil || string(signature) != hex.EncodeToString(mac.Sum(nil)) {
				t.Fatal("allowed token's exact HMAC changed", err)
			}
		})
	}
	headers := http.Header{}
	headers.Set("X-Gitlab-Token", string(strong))
	if _, err := provider.Verify(headers, body, [][]byte{[]byte("hook"), strong}); err != nil {
		t.Fatal("unmatched short key poisoned a strong match", err)
	}
}
