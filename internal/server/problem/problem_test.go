package problem

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	ResponseError(rec, httptest.NewRequest(http.MethodGet, "/stacks", nil), err)
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
		httptest.NewRequest(http.MethodGet, "/", nil),
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
		httptest.NewRequest(http.MethodPost, "/", nil),
		errors.New("can't decode body"),
	)
	if p := decode(t, rec); rec.Code != http.StatusBadRequest || p.Detail != "can't decode body" {
		t.Errorf("got %d %+v", rec.Code, p)
	}
}
