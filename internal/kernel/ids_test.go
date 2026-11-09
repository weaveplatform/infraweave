package kernel

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestUnitULIDMonotonic(t *testing.T) {
	at := time.UnixMilli(1_762_300_000_000)
	ids := make([]ULID, 1000)
	for i := range ids {
		ids[i] = newULIDAt(at)
	}
	if !slices.IsSortedFunc(
		ids,
		func(a, b ULID) int { return strings.Compare(a.String(), b.String()) },
	) {
		t.Error("ULIDs of one millisecond are not in generation order")
	}
	if !ids[0].Time().Equal(at) {
		t.Errorf("Time() = %v, want %v", ids[0].Time(), at)
	}
}

func TestUnitULIDConcurrent(t *testing.T) {
	var mu sync.Mutex
	seen := map[ULID]bool{}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 500 {
				u := NewULID()
				mu.Lock()
				if seen[u] {
					t.Errorf("duplicate ULID %s", u)
				}
				seen[u] = true
				mu.Unlock()
			}
		})
	}
	wg.Wait()
}

func TestUnitULIDText(t *testing.T) {
	u := NewULID()
	s := u.String()
	if len(s) != 26 {
		t.Fatalf("len(%q) = %d, want 26", s, len(s))
	}
	for _, in := range []string{s, strings.ToLower(s)} {
		if got, err := ParseULID(in); err != nil || got != u {
			t.Errorf("ParseULID(%q) = %v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "x", s + "0", "8ZZZZZZZZZZZZZZZZZZZZZZZZZ", strings.Repeat("U", 26)} {
		if _, err := ParseULID(bad); !errors.Is(err, ErrULID) {
			t.Errorf("ParseULID(%q) err = %v, want ErrULID", bad, err)
		}
	}

	b, err := json.Marshal(struct {
		ID ULID `json:"id"`
	}{u})
	if err != nil || string(b) != `{"id":"`+s+`"}` {
		t.Fatalf("json = %s, %v", b, err)
	}
	var back struct {
		ID ULID `json:"id"`
	}
	if err := json.Unmarshal(b, &back); err != nil || back.ID != u {
		t.Errorf("json round trip = %v, %v", back.ID, err)
	}
	if err := json.Unmarshal([]byte(`{"id":"nope"}`), &back); !errors.Is(err, ErrULID) {
		t.Errorf("json of a bad ULID err = %v", err)
	}
}

func TestUnitULIDColumn(t *testing.T) {
	u := NewULID()
	v, err := u.Value()
	if err != nil {
		t.Fatal(err)
	}
	text, ok := v.(string)
	if !ok || len(text) != 36 || text[8] != '-' {
		t.Fatalf("Value() = %#v, want uuid text", v)
	}
	for _, src := range []any{text, []byte(text), u[:], [16]byte(u)} {
		var got ULID
		if err := got.Scan(src); err != nil || got != u {
			t.Errorf("Scan(%T) = %v, %v", src, got, err)
		}
	}

	var z ULID
	if v, err := z.Value(); v != nil || err != nil {
		t.Errorf("zero Value() = %v, %v, want NULL", v, err)
	}
	got := u
	if err := got.Scan(nil); err != nil || !got.IsZero() {
		t.Errorf("Scan(nil) = %v, %v", got, err)
	}
	for _, bad := range []any{
		42,
		"not-a-uuid",
		"zzzzzzzz-zzzz-zzzz-zzzz-zzzzzzzzzzzz",
		"0123456789abcdef0123456789abcdef0123",
	} {
		if err := got.Scan(bad); !errors.Is(err, ErrULID) {
			t.Errorf("Scan(%#v) err = %v, want ErrULID", bad, err)
		}
	}
}

func TestUnitSlug(t *testing.T) {
	if s := Slug("my-stack"); s.String() != "my-stack" {
		t.Errorf("String() = %q", s.String())
	}
	for name, want := range map[string]Slug{
		"My Stack":          "my-stack",
		"  prod / eu-west ": "prod-eu-west",
		"Infra_2026!":       "infra-2026",
		"--a--b--":          "a-b",
		"Zürich":            "z-rich",
		"!!!":               "",
	} {
		if got := SlugFrom(name); got != want {
			t.Errorf("SlugFrom(%q) = %q, want %q", name, got, want)
		}
	}
	for s, want := range map[Slug]bool{
		"my-stack": true, "a1": true, "": false, "My-stack": false,
		"-a": false, "a-": false, "a--b": false, "a_b": false,
	} {
		if got := s.Valid(); got != want {
			t.Errorf("Slug(%q).Valid() = %v, want %v", s, got, want)
		}
	}
}
