package sql

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestUnitUnavailable(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"admin shutdown":     {&pgconn.PgError{Code: "57P01"}, true},
		"crash shutdown":     {&pgconn.PgError{Code: "57P02"}, true},
		"starting up":        {&pgconn.PgError{Code: "57P03"}, true},
		"connection failure": {&pgconn.PgError{Code: "08006"}, true},
		"refused":            {&net.OpError{Op: "dial", Err: errors.New("connection refused")}, true},
		"dropped":            {fmt.Errorf("read: %w", io.ErrUnexpectedEOF), true},
		"closed":             {fmt.Errorf("read: %w", io.EOF), true},
		"acquire timed out":  {fmt.Errorf("acquire: %w", context.DeadlineExceeded), true},
		"unique violation":   {&pgconn.PgError{Code: "23505"}, false},
		"statement timeout":  {&pgconn.PgError{Code: "57014"}, false},
		"no rows":            {pgx.ErrNoRows, false},
		"cancelled":          {context.Canceled, false},
	} {
		t.Run(name, func(t *testing.T) {
			got := Unavailable(fmt.Errorf("store: %w", tc.err))
			if errors.Is(got, kernel.ErrUnavailable) != tc.want || !errors.Is(got, tc.err) {
				t.Errorf("Unavailable(%v) = %v, want unavailable %v", tc.err, got, tc.want)
			}
		})
	}
	if Unavailable(nil) != nil {
		t.Error("Unavailable(nil) is not nil")
	}
}

// TestAccUnavailable checks the classification against a real refused
// connection: a server that does not answer.
func TestAccUnavailable(t *testing.T) {
	c := Defaults()
	c.URL = "postgres://u:p@127.0.0.1:1/x?sslmode=disable&connect_timeout=2"
	_, err := Open(t.Context(), c)
	if err == nil || !errors.Is(Unavailable(err), kernel.ErrUnavailable) {
		t.Errorf("Open on a closed port: %v", err)
	}
}
