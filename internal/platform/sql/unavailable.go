package sql

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// unavailableCodes are the SQLSTATEs of a server that is going away or not
// yet taking connections: admin_shutdown, crash_shutdown and
// cannot_connect_now. Class 08, connection exception, is unavailable too.
var unavailableCodes = map[string]bool{"57P01": true, "57P02": true, "57P03": true}

// Unavailable wraps err with kernel.ErrUnavailable when it says PostgreSQL
// cannot be reached, so the request that hit it answers 503 and is retried:
// a connection that could not be made, a dropped or timed-out connection,
// a pool acquire that timed out, or a server shutting down. Any other
// error, a failed query included, is returned as it is.
func Unavailable(err error) error {
	if err == nil || !unreachable(err) {
		return err
	}
	return fmt.Errorf("%w: %w", kernel.ErrUnavailable, err)
}

func unreachable(err error) bool {
	if _, ok := errors.AsType[*pgconn.ConnectError](err); ok {
		return true
	}
	if pe, ok := errors.AsType[*pgconn.PgError](err); ok {
		return unavailableCodes[pe.Code] || strings.HasPrefix(pe.Code, "08")
	}
	if _, ok := errors.AsType[net.Error](err); ok {
		return true
	}
	return pgconn.Timeout(err) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}
