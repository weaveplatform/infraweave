package azuredevops_test

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

	"github.com/weaveplatform/infraweave/internal/vcs/internal/adapters/azuredevops"
)

func azureBasic(username, password string) http.Header {
	request := http.Request{Header: http.Header{}}
	request.SetBasicAuth(username, password)
	return request.Header
}

func TestUnitAzureDevOpsBasic(t *testing.T) {
	current := []byte("azure-current-password-at-least-32-bytes")
	previous := []byte("azure-previous-password-at-least-32-bytes")
	colon := []byte("azure:password:with:colon-at-least-32-bytes")
	body := []byte(`{"id":"a0a0a0a0-bbbb-cccc-dddd-e1e1e1e1e1e1","eventType":"git.push"}`)
	provider := azuredevops.New(azuredevops.Options{})
	for _, tc := range []struct {
		name    string
		headers http.Header
		allowed [][]byte
		matched []byte
	}{
		{"current", azureBasic("", string(current)), [][]byte{current, previous}, current},
		{"previous", azureBasic("", string(previous)), [][]byte{current, previous}, previous},
		{"current after previous", azureBasic("", string(current)), [][]byte{previous, current}, current},
		{"colon password", azureBasic("", string(colon)), [][]byte{colon}, colon},
		{"missing", nil, [][]byte{current}, nil},
		{"wrong password", azureBasic("", "wrong"), [][]byte{current, previous}, nil},
		{"nonempty username", azureBasic("pat", string(current)), [][]byte{current, previous}, nil},
		{"empty password", azureBasic("", ""), [][]byte{nil, {}}, nil},
		{"expired previous", azureBasic("", string(previous)), [][]byte{current}, nil},
		{"no allowed keys", azureBasic("", string(current)), nil, nil},
		{"wrong scheme", http.Header{"Authorization": {"Bearer unrelated"}}, [][]byte{current}, nil},
		{"malformed base64", http.Header{"Authorization": {"Basic !!!"}}, [][]byte{current}, nil},
		{"missing colon", http.Header{"Authorization": {"Basic cGFzc3dvcmQ="}}, [][]byte{current}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			signature, err := provider.Verify(tc.headers, body, tc.allowed)
			if tc.matched == nil {
				if err == nil || signature != nil {
					t.Fatal("unverified Basic credential returned a signature")
				}
				return
			}
			mac := hmac.New(sha256.New, tc.matched)
			_, _ = mac.Write(body)
			if err != nil || string(signature) != hex.EncodeToString(mac.Sum(nil)) || bytes.Equal(signature, tc.matched) {
				t.Fatal("stored digest did not use the matched raw password", err)
			}
			changed, err := provider.Verify(tc.headers, append(bytes.Clone(body), '\n'), tc.allowed)
			if err != nil || hmac.Equal(signature, changed) {
				t.Fatal("retained digest did not bind exact body bytes", err)
			}
		})
	}
	headers := azureBasic("", string(current))
	headers.Add("Authorization", headers.Get("Authorization"))
	if signature, err := provider.Verify(headers, body, [][]byte{current}); err == nil || signature != nil {
		t.Fatal("ambiguous authorization headers accepted")
	}
}

func TestUnitAzureDevOpsShortPasswordPolicy(t *testing.T) {
	provider := azuredevops.New(azuredevops.Options{})
	body := []byte(`{"eventType":"git.push"}`)
	strong := []byte("azure-strong-password-at-least-32-bytes")
	for _, password := range []string{"hook", strings.Repeat("x", 13), strings.Repeat("x", 14)} {
		t.Run(fmt.Sprint(len(password)), func(t *testing.T) {
			signature, err := provider.Verify(azureBasic("", password), body, [][]byte{strong, []byte(password)})
			if fips140.Enforced() && len(password) < 14 {
				if err == nil || signature != nil {
					t.Fatal("disallowed HMAC key not refused")
				}
				return
			}
			mac := hmac.New(sha256.New, []byte(password))
			_, _ = mac.Write(body)
			if err != nil || string(signature) != hex.EncodeToString(mac.Sum(nil)) {
				t.Fatal("allowed raw-key HMAC changed", err)
			}
		})
	}
	for _, allowed := range [][][]byte{{[]byte("hook"), strong}, {strong, []byte("hook")}} {
		if _, err := provider.Verify(azureBasic("", string(strong)), body, allowed); err != nil {
			t.Fatal("unmatched short password poisoned a strong match", err)
		}
	}
}
