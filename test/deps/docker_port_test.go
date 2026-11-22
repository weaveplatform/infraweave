package deps

import (
	"errors"
	"testing"
)

func TestPortTaken(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "bind refused",
			err:  errors.New("container start: failed to bind host port 127.0.0.1:44155/tcp: address already in use"),
			want: true,
		},
		{
			name: "port allocated",
			err:  errors.New("Bind for 127.0.0.1:44155 failed: port is already allocated"),
			want: true,
		},
		{
			name: "other failure",
			err:  errors.New("@postgres: migrate: relation exists"),
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := portTaken(tc.err); got != tc.want {
				t.Fatalf("portTaken(%q) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
