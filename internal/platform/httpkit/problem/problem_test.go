package problem

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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
	if rec.Code != http.StatusNotImplemented || p.Title != "Not Implemented" ||
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
// complete list of slugs (decision 0022), in both directions, so the server
// and every client that branches on Types agree with it.
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
