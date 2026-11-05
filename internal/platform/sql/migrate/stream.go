package migrate

import (
	"bufio"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
)

// Stream is one domain's migrations, or the platform's: a name and the
// files at the root of Files, named NNNNN_snake_case.sql, five digits and
// gap-free from 00001.
type Stream struct {
	Name  string
	Files fs.FS
	// Floor is the newest version of this stream in the last minor release
	// of the previous major (decision 0020). A database below it would skip
	// a major, which is refused. The zero Floor refuses nothing.
	Floor Floor
}

// Floor is a stream's upgrade floor and the release that reaches it.
type Floor struct {
	Version int
	Release string
}

// File is one migration of a stream.
type File struct {
	Version int
	// Name is the file name without the number and the .sql suffix.
	Name     string
	SQL      string
	Checksum []byte
	// NoTransaction is the -- infraweave:no-transaction header: the file
	// runs outside a transaction, one statement at a time, for CREATE INDEX
	// CONCURRENTLY.
	NoTransaction bool
	// CompatibleFrom is the -- infraweave:compatible-from header's version,
	// or 0: from this file on, a binary whose newest version of the stream is
	// older refuses to start.
	CompatibleFrom int
}

// ErrStream marks a stream whose files break the naming or header rules.
var ErrStream = errors.New("invalid migration stream")

var (
	fileName  = regexp.MustCompile(`^(\d{5})_([a-z0-9]+(?:_[a-z0-9]+)*)\.sql$`)
	streamKey = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

const (
	directive      = "-- infraweave:"
	noTransaction  = "no-transaction"
	compatibleFrom = "compatible-from "
)

// Load reads and checks the stream's files, in version order.
func (s Stream) Load() ([]File, error) {
	if !streamKey.MatchString(s.Name) {
		return nil, fmt.Errorf("%w: name %q is not lower snake case", ErrStream, s.Name)
	}
	entries, err := fs.ReadDir(s.Files, ".")
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrStream, s.Name, err)
	}
	var files []File
	for _, e := range entries {
		m := fileName.FindStringSubmatch(e.Name())
		if e.IsDir() || m == nil {
			return nil, fmt.Errorf(
				"%w: %s: %s is not NNNNN_snake_case.sql",
				ErrStream,
				s.Name,
				e.Name(),
			)
		}
		v, _ := strconv.Atoi(m[1]) // five digits always parse
		if want := len(files) + 1; v != want {
			return nil, fmt.Errorf(
				"%w: %s: %s where version %05d belongs",
				ErrStream,
				s.Name,
				e.Name(),
				want,
			)
		}
		b, err := fs.ReadFile(s.Files, e.Name())
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrStream, s.Name, err)
		}
		sum := sha256.Sum256(b)
		f := File{Version: v, Name: m[2], SQL: string(b), Checksum: sum[:]}
		if why := f.readHeader(); why != "" {
			return nil, fmt.Errorf("%w: %s: %s: %s", ErrStream, s.Name, e.Name(), why)
		}
		files = append(files, f)
	}
	if s.Floor.Version > len(files) {
		return nil, fmt.Errorf("%w: %s: upgrade floor %05d is past the newest file %05d",
			ErrStream, s.Name, s.Floor.Version, len(files))
	}
	if s.Floor.Version > 0 && s.Floor.Release == "" {
		return nil, fmt.Errorf(
			"%w: %s: upgrade floor %05d names no release",
			ErrStream,
			s.Name,
			s.Floor.Version,
		)
	}
	return files, nil
}

// readHeader reads the directives in the file's leading comment lines, and
// returns why they are wrong, if they are. An unknown directive is wrong
// rather than a comment, so a misspelt no-transaction never runs a file
// inside a transaction unnoticed.
func (f *File) readHeader() string {
	sc := bufio.NewScanner(strings.NewReader(f.SQL))
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(text, "--") {
			return ""
		}
		d, ok := strings.CutPrefix(text, directive)
		switch {
		case !ok:
		case d == noTransaction && line == 1:
			f.NoTransaction = true
		case d == noTransaction:
			return "-- infraweave:no-transaction must be the first line"
		case strings.HasPrefix(d, compatibleFrom):
			v, err := strconv.Atoi(strings.TrimPrefix(d, compatibleFrom))
			if err != nil || v < 1 || v > f.Version {
				return fmt.Sprintf("compatible-from %q is not a version up to %05d",
					strings.TrimPrefix(d, compatibleFrom), f.Version)
			}
			f.CompatibleFrom = v
		default:
			return fmt.Sprintf("unknown directive %q", text)
		}
	}
	return ""
}

// statements splits a no-transaction file at each semicolon that ends a
// line. PostgreSQL runs a multi-statement query string as one implicit
// transaction, which CREATE INDEX CONCURRENTLY refuses, so each statement is
// sent alone. Such a file holds plain DDL: no function bodies, whose
// semicolons this would split.
func (f File) statements() []string {
	var out []string
	var cur strings.Builder
	for line := range strings.Lines(f.SQL) {
		cur.WriteString(line)
		if strings.HasSuffix(strings.TrimSpace(line), ";") {
			if stmt := strings.TrimSpace(cur.String()); !onlyComments(stmt) {
				out = append(out, stmt)
			}
			cur.Reset()
		}
	}
	if stmt := strings.TrimSpace(cur.String()); !onlyComments(stmt) {
		out = append(out, stmt)
	}
	return out
}

func onlyComments(s string) bool {
	for line := range strings.Lines(s) {
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "--") {
			return false
		}
	}
	return true
}
