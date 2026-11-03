// Package pagination is decision 0003's cursor pagination for family
// handlers: the page size from limit, and opaque cursors that hold a keyset
// position. A list reads WHERE (sort_key, id) > (cursor) ORDER BY sort_key,
// id LIMIT limit + 1, and encodes the last row it returns as the next cursor
// when the extra row shows there is another page.
package pagination

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/weaveplatform/infraweave/internal/server/problem"
)

const (
	// DefaultLimit is the page size when a request names none.
	DefaultLimit = 20
	// MaxLimit is the largest page.
	MaxLimit = 100
)

// Limit is the page size for a requested limit: DefaultLimit when absent,
// otherwise clamped to 1 to MaxLimit, never rejected.
func Limit(requested *int32) int32 {
	if requested == nil {
		return DefaultLimit
	}
	return min(max(*requested, 1), MaxLimit)
}

// version is the cursor encoding. A cursor of another version is rejected
// rather than misread; a new encoding keeps decoding this one until no
// client can still hold it.
const version = 1

// Position is the last row of a page: its sort key and its ID, the
// tie-breaker that makes the order total.
type Position[K any] struct {
	Key K
	ID  string
}

// wire is a cursor's JSON as Decode first reads it, the key left raw until
// the rest checks out. List names the list the cursor came from, so a cursor
// from another list is refused instead of read as a position in this one.
type wire struct {
	V    int             `json:"v"`
	List string          `json:"l"`
	Key  json.RawMessage `json:"k"`
	ID   string          `json:"id"`
}

// Encode is the cursor for p in list, base64url-encoded versioned JSON.
func Encode[K any](list string, p Position[K]) (string, error) {
	b, err := json.Marshal(struct {
		V    int    `json:"v"`
		List string `json:"l"`
		Key  K      `json:"k"`
		ID   string `json:"id"`
	}{version, list, p.Key, p.ID})
	if err != nil {
		return "", fmt.Errorf("encode cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Decode reads a cursor of list. Its error is a *problem.Error of type
// invalid-cursor, for the handler to return as it is.
func Decode[K any](list, cursor string) (Position[K], error) {
	var p Position[K]
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return p, invalid("The cursor is not base64url.")
	}
	var w wire
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil || !atEOF(dec) {
		return p, invalid("The cursor does not decode.")
	}
	switch {
	case w.V != version:
		return p, invalid(fmt.Sprintf("The cursor's version %d is not served.", w.V))
	case w.List != list:
		return p, invalid("The cursor belongs to another list.")
	case w.ID == "" || len(w.Key) == 0:
		return p, invalid("The cursor holds no position.")
	}
	if err := json.Unmarshal(w.Key, &p.Key); err != nil {
		return Position[K]{}, invalid("The cursor's sort key does not fit this list.")
	}
	p.ID = w.ID
	return p, nil
}

func atEOF(dec *json.Decoder) bool {
	_, err := dec.Token()
	return errors.Is(err, io.EOF)
}

// invalid is returned unwrapped: a handler hands it to ResponseError as it
// is, which answers with the problem it carries.
func invalid(detail string) error {
	return &problem.Error{Problem: problem.InvalidCursor.New(detail)}
}
