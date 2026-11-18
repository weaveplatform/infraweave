package port_test

import (
	"errors"
	"net/http"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestUnitCheckOriginAliasesAndSecrecy(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"https://GIT.EXAMPLE.COM:443/git/api/", "https://git.example.com/git/api"},
		{"http://GIT.EXAMPLE.COM:80/api/", "http://git.example.com/api"},
		{"https://[2001:DB8::1]:443/api/", "https://[2001:db8::1]/api"},
		{"https://git.example.com:8443/private/api", "https://git.example.com:8443/private/api"},
	} {
		if got, err := port.CheckOrigin(tc.raw); err != nil || got != tc.want {
			t.Fatal("native origin alias split its resource fence", got, tc.want, err)
		}
	}
	for _, raw := range []string{"https://private-user:private-password@git.example.com/api", "https://git.example.com/api?token=private", "https://git.example.com/api#private", "file:///private", "relative", "https://%zz"} {
		if got, err := port.CheckOrigin(raw); got != "" || !errors.Is(err, port.ErrCheckInput) || err.Error() != port.ErrCheckInput.Error() {
			t.Fatal("invalid native origin retained private URL contents", got, err)
		}
	}
}

func TestUnitCheckResetHeaders(t *testing.T) {
	now := time.Unix(2000000000, 0)
	for _, tc := range []struct {
		name, value string
		wait        time.Duration
		valid       bool
	}{
		{"X-RateLimit-Reset", "2000000120", 2 * time.Minute, true},
		{"RateLimit-Reset", "2000000120", 2 * time.Minute, true},
		{"Retry-After", "120", 2 * time.Minute, true},
		{"Retry-After", now.Add(time.Minute).UTC().Format(http.TimeFormat), time.Minute, true},
		{"Retry-After", "-1", 0, false},
		{"X-RateLimit-Reset", "invalid", 0, false},
		{"Retry-After", "", 0, false},
	} {
		h := http.Header{}
		h.Set(tc.name, tc.value)
		reset, ok := port.RetryReset(h, now)
		if ok != tc.valid || ok && !reset.Equal(now.Add(tc.wait)) {
			t.Fatal(tc, reset, ok)
		}
	}
	if got := port.CheckText("abc世界def", 9); got != "abc世..." || !utf8.ValidString(got) {
		t.Fatal(got)
	}
}
