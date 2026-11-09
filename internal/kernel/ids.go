package kernel

import (
	"crypto/rand"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

// ULID identifies a row: generated in Go, stored in a uuid column and
// written on the wire as 26 characters of Crockford base32. The zero ULID
// is the absent ID.
//
// ref: decision 0005
type ULID [16]byte

// ErrULID reports text or a column value that is no ULID.
var ErrULID = errors.New("not a ULID")

// ulid.Monotonic is not safe for concurrent use, and one reader shared
// under a lock is what keeps IDs from one millisecond in generation order
// across goroutines; ulid.Make's pool of readers does not.
var (
	ulidMu      sync.Mutex
	ulidEntropy = ulid.Monotonic(rand.Reader, 0)
)

// NewULID returns a ULID for the current millisecond, greater than every
// ULID this process generated before it in the same millisecond.
func NewULID() ULID {
	return newULIDAt(time.Now())
}

func newULIDAt(t time.Time) ULID {
	ulidMu.Lock()
	defer ulidMu.Unlock()
	// MustNew panics only when the monotonic entropy overflows, after about
	// 2^48 IDs in one millisecond.
	return ULID(ulid.MustNew(ulid.Timestamp(t), ulidEntropy))
}

// ParseULID reads the 26-character wire form, in either case.
func ParseULID(s string) (ULID, error) {
	u, err := ulid.ParseStrict(s)
	if err != nil {
		return ULID{}, fmt.Errorf("%w: %q", ErrULID, s)
	}
	return ULID(u), nil
}

// String is the 26-character wire form.
func (u ULID) String() string { return ulid.ULID(u).String() }

// IsZero reports the absent ID.
func (u ULID) IsZero() bool { return u == ULID{} }

// Time is the millisecond the ULID was generated in.
func (u ULID) Time() time.Time { return ulid.Time(ulid.ULID(u).Time()) }

// MarshalText writes the wire form, which JSON uses too.
func (u ULID) MarshalText() ([]byte, error) { return []byte(u.String()), nil }

// UnmarshalText reads the wire form.
func (u *ULID) UnmarshalText(b []byte) error {
	v, err := ParseULID(string(b))
	if err != nil {
		return err
	}
	*u = v
	return nil
}

// Value writes the ULID as the text of a uuid, which pgx encodes into a
// uuid column; the zero ULID writes NULL. Implementing database/sql's
// interfaces keeps this package free of a driver import.
func (u ULID) Value() (driver.Value, error) {
	if u.IsZero() {
		return nil, nil //nolint:nilnil // a nil driver.Value is SQL NULL
	}
	return u.UUID(), nil
}

// Scan reads a uuid column as pgx hands it to a database/sql Scanner: its
// text, or its 16 bytes. NULL leaves the zero ULID.
func (u *ULID) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*u = ULID{}
		return nil
	case string:
		return u.scanUUID(v)
	case []byte:
		if len(v) == len(u) {
			copy(u[:], v)
			return nil
		}
		return u.scanUUID(string(v))
	case [16]byte:
		*u = ULID(v)
		return nil
	default:
		return fmt.Errorf("%w: column value of type %T", ErrULID, src)
	}
}

func (u *ULID) scanUUID(s string) error {
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return fmt.Errorf("%w: uuid %q", ErrULID, s)
	}
	h := s[0:8] + s[9:13] + s[14:18] + s[19:23] + s[24:36]
	var b ULID
	if _, err := hex.Decode(b[:], []byte(h)); err != nil {
		return fmt.Errorf("%w: uuid %q", ErrULID, s)
	}
	*u = b
	return nil
}

// UUID is the ULID as PostgreSQL writes its uuid column's text, which is
// how a sealed column's associated data names a uuid key, since the
// reseal walk reads every key as text.
func (u ULID) UUID() string {
	h := hex.EncodeToString(u[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// Slug is the account-unique name that stacks, modules, spaces, contexts,
// policies, templates and blueprints are addressed by, beside their ULID
// key: lowercase letters and digits in runs joined by single hyphens, as
// SlugFrom derives one from a name.
//
// ref: decision 0005
type Slug string

// String is the slug as written on the wire.
func (s Slug) String() string { return string(s) }

// SlugFrom derives a slug from name: lowercased, each run of anything
// other than a letter a to z or a digit one hyphen, with none leading or
// trailing. A name with no such letter or digit derives the empty slug,
// which is not Valid.
func SlugFrom(name string) Slug {
	var b strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if hyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			hyphen = false
			b.WriteRune(r)
			continue
		}
		hyphen = true
	}
	return Slug(b.String())
}

// Valid reports whether s is a slug SlugFrom could derive: not empty, and
// the same once derived again.
func (s Slug) Valid() bool { return s != "" && SlugFrom(string(s)) == s }
