package strict

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Merge is target with patch applied as RFC 7396 defines: an object patch
// sets each member it names, recursing into objects, and a null member
// removes it; any other patch replaces target whole, arrays included.
// Neither argument is changed.
func Merge(target, patch any) any {
	p, ok := patch.(map[string]any)
	if !ok {
		return patch
	}
	t, ok := target.(map[string]any)
	out := make(map[string]any, len(t)+len(p))
	if ok {
		for k, v := range t {
			out[k] = v
		}
	}
	for k, v := range p {
		if v == nil {
			delete(out, k)
			continue
		}
		out[k] = Merge(out[k], v)
	}
	return out
}

// Patch is view, an input view, with patch merged into it, decoded into the
// input's type. A member the patch sets to null is absent from the result,
// so the PUT the result runs through resets it to its default.
func Patch[T any](view T, patch map[string]any) (T, error) {
	var out T
	b, err := json.Marshal(view)
	if err != nil {
		return out, fmt.Errorf("strict: encode the input view: %w", err)
	}
	var doc any
	if err := json.Unmarshal(b, &doc); err != nil {
		return out, fmt.Errorf("strict: decode the input view: %w", err)
	}
	if b, err = json.Marshal(Merge(doc, patch)); err != nil {
		return out, fmt.Errorf("strict: encode the merged input: %w", err)
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return out, fmt.Errorf("strict: decode the merged input: %w", err)
	}
	return out, nil
}

// patchAttempts bounds Apply's reads: each retry follows a write that
// another request made between this one's read and write, so three
// collisions in a row are contention to report, not to wait out.
const patchAttempts = 3

// Apply runs a PATCH through its PUT. It reads the entity's input view and
// version, checks the client's If-Match against the version, merges patch
// into the view and writes the result expecting exactly the version read,
// so a change made in between is never overwritten unseen. When another
// write wins that race and the client sent no If-Match, it reads again;
// with one, the client sees kernel.ErrStale, since the version it named is
// gone.
func Apply[V, R any](
	expect kernel.Expect,
	patch map[string]any,
	read func() (V, int64, error),
	write func(V, kernel.Expect) (R, error),
) (R, error) {
	var zero R
	for range patchAttempts {
		view, version, err := read()
		if err != nil {
			return zero, err
		}
		if err := expect.Check(version); err != nil {
			return zero, err //nolint:wrapcheck // kernel.ErrStale, which the HTTP layer answers with 412
		}
		in, err := Patch(view, patch)
		if err != nil {
			return zero, err
		}
		out, err := write(in, kernel.ExpectVersions(version))
		if !errors.Is(err, kernel.ErrStale) || expect.Conditional() {
			return out, err
		}
	}
	return zero, kernel.ErrStale
}

// ETag is version's strong entity tag.
func ETag(version int64) string { return `"` + strconv.FormatInt(version, 10) + `"` }

// IfMatch is the versions an If-Match header accepts: any when it is
// absent or *, otherwise the versions of its strong entity tags. A weak
// tag never matches, since If-Match compares strongly, and a tag that names
// no version matches none.
func IfMatch(header *string) kernel.Expect {
	if header == nil || strings.TrimSpace(*header) == "" {
		return kernel.Expect{}
	}
	if strings.TrimSpace(*header) == "*" {
		return kernel.ExpectAny()
	}
	var versions []int64
	for tag := range strings.SplitSeq(*header, ",") {
		tag = strings.TrimSpace(tag)
		if len(tag) < 2 || tag[0] != '"' || tag[len(tag)-1] != '"' {
			continue
		}
		if v, err := strconv.ParseInt(tag[1:len(tag)-1], 10, 64); err == nil {
			versions = append(versions, v)
		}
	}
	return kernel.ExpectVersions(versions...)
}
