package problem

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/kernel/search"
)

func decode(t *testing.T, rec *httptest.ResponseRecorder) Problem {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != ContentType {
		t.Fatalf("Content-Type = %q", ct)
	}
	var p Problem
	if err := json.NewDecoder(rec.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	if p.Status != rec.Code {
		t.Errorf("body status %d, status line %d", p.Status, rec.Code)
	}
	return p
}

func TestUnitResponseErrorNotImplemented(t *testing.T) {
	rec := httptest.NewRecorder()
	err := fmt.Errorf(
		"wrapped: %w",
		&NotImplementedError{OperationID: "stacks", Route: "GET /stacks"},
	)
	ResponseError(
		rec,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/stacks", nil),
		err,
	)
	p := decode(t, rec)
	if rec.Code != http.StatusNotImplemented ||
		!reflect.DeepEqual(p, NotImplemented.New("stacks (GET /stacks) is not implemented yet")) ||
		p.Detail != "stacks (GET /stacks) is not implemented yet" {
		t.Errorf("got %d %+v", rec.Code, p)
	}
}

func TestUnitResponseErrorInternal(t *testing.T) {
	rec := httptest.NewRecorder()
	ResponseError(
		rec,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil),
		errors.New("database password is hunter2"),
	)
	p := decode(t, rec)
	if rec.Code != http.StatusInternalServerError || strings.Contains(p.Detail, "hunter2") {
		t.Errorf("internal error leaked or wrong status: %d %+v", rec.Code, p)
	}
}

func TestUnitResponseErrorStale(t *testing.T) {
	rec := httptest.NewRecorder()
	ResponseError(
		rec,
		httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/", nil),
		fmt.Errorf("update: %w", kernel.ErrStale),
	)
	if p := decode(t, rec); rec.Code != http.StatusPreconditionFailed ||
		p.Type != TypePrefix+PreconditionFailed.Slug {
		t.Errorf("got %d %+v", rec.Code, p)
	}
}

func TestUnitRequestError(t *testing.T) {
	rec := httptest.NewRecorder()
	RequestError(
		rec,
		httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil),
		errors.New("can't decode body"),
	)
	if p := decode(t, rec); rec.Code != http.StatusBadRequest || p.Detail != "can't decode body" {
		t.Errorf("got %d %+v", rec.Code, p)
	}
}

func TestUnitResponseErrorProblem(t *testing.T) {
	rec := httptest.NewRecorder()
	err := fmt.Errorf("list: %w", InvalidCursor.Error("The cursor belongs to another list."))
	ResponseError(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), err)
	p := decode(t, rec)
	if rec.Code != http.StatusBadRequest || p.Type != TypePrefix+"invalid-cursor" ||
		p.Title != "Invalid cursor" || p.Detail != "The cursor belongs to another list." {
		t.Errorf("got %d %+v", rec.Code, p)
	}
	if got := err.Error(); got != "list: Invalid cursor: The cursor belongs to another list." {
		t.Errorf("Error() = %q", got)
	}
}

func TestUnitResponseErrorSearch(t *testing.T) {
	rec := httptest.NewRecorder()
	err := fmt.Errorf(
		"search spaces: %w",
		&search.Invalid{Detail: "bad", Errors: []search.FieldError{
			{Pointer: "/predicates/0/field", Reason: search.ReasonUnknownField, Message: "no"},
		}},
	)
	ResponseError(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil), err)
	var p Problem
	if err := json.NewDecoder(rec.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	want := []FieldError{{Pointer: "/predicates/0/field", Reason: "unknown-field", Message: "no"}}
	if rec.Code != http.StatusUnprocessableEntity || p.Type != TypePrefix+"validation-failed" ||
		p.Detail != "bad" || !reflect.DeepEqual(p.Errors, want) {
		t.Errorf("got %d %+v", rec.Code, p)
	}
}

// registryDiff names each difference between the declared types and the
// register's records: a slug only one side has, or a status or title that
// differs.
func registryDiff(types, records []Type) []string {
	declared := map[string]Type{}
	for _, t := range types {
		declared[t.Slug] = t
	}
	var out []string
	for _, r := range records {
		t, ok := declared[r.Slug]
		switch {
		case !ok:
			out = append(out, r.Slug+": in the register, not declared")
		case t != r:
			out = append(out, fmt.Sprintf("%s: declared %+v, register has %+v", r.Slug, t, r))
		}
		delete(declared, r.Slug)
	}
	for _, t := range types {
		if _, ok := declared[t.Slug]; ok {
			out = append(out, t.Slug+": declared, not in the register")
		}
	}
	return out
}

// TestUnitProblemRegistry holds Types to the problem register, the one
// complete list of slugs, in both directions, so the server and every
// client that branches on Types agree with it.
//
// ref: decision 0022
func TestUnitProblemRegistry(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "docs", "data", "problems.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		Records []struct {
			ID     string `json:"id"`
			Status int    `json:"status"`
			Title  string `json:"title"`
		} `json:"records"`
	}
	if err := json.Unmarshal(b, &reg); err != nil {
		t.Fatal(err)
	}
	records := make([]Type, 0, len(reg.Records))
	for _, r := range reg.Records {
		records = append(records, Type{r.ID, r.Status, r.Title})
	}
	types := Types()
	for _, d := range registryDiff(types, records) {
		t.Error(d)
	}
	if !slices.IsSortedFunc(types, func(a, b Type) int { return strings.Compare(a.Slug, b.Slug) }) {
		t.Error("Types is not ordered by slug")
	}

	got := registryDiff(
		[]Type{
			NotFound,
			{"teapot", http.StatusTeapot, "teapot"},
			{"forbidden", http.StatusForbidden, "nope"},
		},
		[]Type{NotFound, Forbidden, Unauthenticated},
	)
	want := []string{
		`forbidden: declared {Slug:forbidden Status:403 Title:nope}, register has {Slug:forbidden Status:403 Title:unauthorized: permission denied}`,
		"unauthenticated: in the register, not declared",
		"teapot: declared, not in the register",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestUnitWriteInstance checks that a problem names the request it answers
// from the X-Request-Id on the response, and keeps an instance it was
// given.
func TestUnitWriteInstance(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.Header().Set(RequestIDHeader, "req-7")
	Write(rec, NotFound.New("gone"))
	if p := decode(t, rec); p.Instance != "urn:infraweave:request:req-7" {
		t.Errorf("instance %q", p.Instance)
	}
	rec = httptest.NewRecorder()
	rec.Header().Set(RequestIDHeader, "req-7")
	given := NotFound.New("gone")
	given.Instance = "urn:infraweave:request:earlier"
	Write(rec, given)
	if p := decode(t, rec); p.Instance != given.Instance {
		t.Errorf("instance %q, want the given one", p.Instance)
	}
	rec = httptest.NewRecorder()
	Write(rec, NotFound.New("gone"))
	if p := decode(t, rec); p.Instance != "" {
		t.Errorf("instance %q outside a request", p.Instance)
	}
}

// TestUnitResponseErrorUnavailable checks that an unreachable dependency
// answers 503 with Retry-After and a detail that does not name it.
func TestUnitResponseErrorUnavailable(t *testing.T) {
	rec := httptest.NewRecorder()
	ResponseError(
		rec,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil),
		fmt.Errorf("list stacks: %w: dial tcp 10.0.0.7:5432", kernel.ErrUnavailable),
	)
	p := decode(t, rec)
	if rec.Code != http.StatusServiceUnavailable || p.Type != TypePrefix+"service-unavailable" ||
		rec.Header().Get("Retry-After") != "2" || strings.Contains(p.Detail, "10.0.0.7") {
		t.Errorf("got %d %v %+v", rec.Code, rec.Header(), p)
	}
}

// TestUnitProblemMembers checks the extension members of the feature, tier
// and limit refusals and of field errors, and that a limit of 0 still names
// its value.
//
// ref: decisions 0021 and 0022
func TestUnitProblemMembers(t *testing.T) {
	p := FeatureNotInTier.New("no")
	p.Feature, p.Tiers = "policies", []string{"Starter", "Business"}
	l := NewLimitReached("seats", "account", 0)
	v := ValidationFailed.New("no")
	v.Errors = []FieldError{{Pointer: "/predicates/0/field", Reason: "unknown-field", Message: "m"}}
	for _, c := range []struct {
		p    Problem
		want []string
		not  []string
	}{
		{p, []string{`"feature":"policies"`, `"tiers":["Starter","Business"]`}, []string{`"limit"`, `"value"`}},
		{l, []string{`"limit":"seats"`, `"scope":"account"`, `"value":0`}, []string{`"feature"`, `"tiers"`, `"errors"`}},
		{v, []string{`"errors":[{"pointer":"/predicates/0/field","reason":"unknown-field","message":"m"}]`}, nil},
	} {
		b, err := json.Marshal(c.p)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range c.want {
			if !strings.Contains(string(b), w) {
				t.Errorf("%s lacks %s", b, w)
			}
		}
		for _, n := range c.not {
			if strings.Contains(string(b), n) {
				t.Errorf("%s carries %s", b, n)
			}
		}
	}
}

func TestUnitNewLimitReached(t *testing.T) {
	for scope, detail := range map[string]string{
		"organization": "The organization has reached its seats limit of 2.",
		"account":      "This account has reached its seats limit of 2.",
	} {
		p := NewLimitReached("seats", scope, 2)
		typed := p.Status == http.StatusForbidden && p.Type == LimitReached.New("").Type
		members := p.Limit == "seats" && p.Scope == scope && p.Value != nil && *p.Value == 2
		if !typed || !members || p.Detail != detail {
			t.Errorf("%s: %+v", scope, p)
		}
	}
}
