package api

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// TestUnitSourceCredential checks that a source credential's every
// formatting, and its log value, leave its token out.
func TestUnitSourceCredential(t *testing.T) {
	c := SourceCredential{Username: "x-access", Token: []byte("s3cr3t"), Narrowed: true}
	s := fmt.Sprintf("%v %+v %#v %s", c, c, c, c)
	if strings.Contains(s, "s3cr3t") || !strings.Contains(s, "x-access") {
		t.Errorf("formatted = %s", s)
	}
	var out bytes.Buffer
	slog.New(slog.NewJSONHandler(&out, nil)).Info("c", slog.Any("credential", c))
	if strings.Contains(out.String(), "s3cr3t") ||
		!strings.Contains(out.String(), `"narrowed":true`) {
		t.Errorf("logged = %s", out.String())
	}
}
