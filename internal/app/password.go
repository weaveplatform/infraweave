package app

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	accessassembly "github.com/weaveplatform/infraweave/internal/access/assembly"
)

// ErrNoPassword reports standard input that holds no password.
var ErrNoPassword = errors.New("no password on standard input")

// HashPassword is infraweave auth hash-password: it reads the password as
// the first line of in, so it never appears in a process list or shell
// history, and writes its break-glass hash and a newline to out. Only the
// line's end is trimmed: spaces are part of the password.
func HashPassword(in io.Reader, out io.Writer) error {
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read the password: %w", err)
	}
	pw := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if pw == "" {
		return ErrNoPassword
	}
	h, err := accessassembly.HashPassword(pw)
	if err != nil {
		return err //nolint:wrapcheck // names itself
	}
	_, err = fmt.Fprintln(out, h)
	return err //nolint:wrapcheck // a write to the caller's writer
}
