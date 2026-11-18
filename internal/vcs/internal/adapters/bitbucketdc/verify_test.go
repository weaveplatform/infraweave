package bitbucketdc_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/bitbucketdc"
)

func TestUnitBitbucketDCSignature(t *testing.T) {
	t.Parallel()
	body := []byte(`{"private":"value"}`)
	current, previous := []byte("current-bitbucket-secret-32-bytes"), []byte("previous-bitbucket-secret-32-bytes")
	sign := func(secret []byte) string {
		m := hmac.New(sha256.New, secret)
		_, _ = m.Write(body)
		return "sha256=" + hex.EncodeToString(m.Sum(nil))
	}
	p := bitbucketdc.New(bitbucketdc.Options{})
	for _, tc := range []struct {
		name, header string
		keys         [][]byte
		ok           bool
	}{
		{"current", sign(current), [][]byte{current, previous}, true},
		{"previous", sign(previous), [][]byte{current, previous}, true},
		{"no key", sign(current), nil, false},
		{"wrong", sign(current), [][]byte{previous}, false},
		{"unsigned", "", [][]byte{current}, false},
		{"SHA1", "sha1=bad", [][]byte{current}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := http.Header{}
			if tc.header != "" {
				h.Set("X-Hub-Signature", tc.header)
			}
			got, err := p.Verify(h, body, tc.keys)
			if tc.ok {
				if err != nil || string(got) != tc.header {
					t.Fatalf("verification did not retain exact header: %v", err)
				}
			} else if !errors.Is(err, bitbucketdc.ErrSignature) || len(got) != 0 {
				t.Fatal("invalid signature accepted")
			}
		})
	}
}
