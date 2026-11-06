package app

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"testing/iotest"

	accessassembly "github.com/weaveplatform/infraweave/internal/access/assembly"
)

// TestUnitHashPassword checks that the printed hash verifies the first
// line of standard input, its line end trimmed and its spaces kept, and
// no other password.
func TestUnitHashPassword(t *testing.T) {
	for _, in := range []string{" pass word \n", " pass word \r\nsecond line", " pass word "} {
		var out bytes.Buffer
		if err := HashPassword(strings.NewReader(in), &out); err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		h, ok := strings.CutSuffix(out.String(), "\n")
		if !ok || !accessassembly.VerifyPassword(h, " pass word ") ||
			accessassembly.VerifyPassword(h, "pass word") {
			t.Errorf("%q printed %q", in, out.String())
		}
	}
	if accessassembly.VerifyPassword("$md5$x", "x") {
		t.Error("an unparseable hash matched")
	}
	for _, in := range []string{"", "\n"} {
		if err := HashPassword(
			strings.NewReader(in),
			&bytes.Buffer{},
		); !errors.Is(
			err,
			ErrNoPassword,
		) {
			t.Errorf("%q: %v", in, err)
		}
	}
	boom := errors.New("boom")
	if err := HashPassword(iotest.ErrReader(boom), &bytes.Buffer{}); !errors.Is(err, boom) {
		t.Errorf("unreadable input: %v", err)
	}
}
