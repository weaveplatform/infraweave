package port_test

import (
	"errors"
	"testing"

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
