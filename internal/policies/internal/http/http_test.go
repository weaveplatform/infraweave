package http

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

func TestUnitContractPublisher(t *testing.T) {
	server := New(nil, nil)
	for range 2 {
		r := chi.NewRouter()
		server.Routes(r)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, ContractPattern, nil))
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
	}
	r := chi.NewRouter()
	Server{contract: func() ([]byte, error) { return nil, errors.New("internal schema construction detail") }}.Routes(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, ContractPattern, nil))
	if rec.Code != http.StatusInternalServerError || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestUnitContractConstructionFailures(t *testing.T) {
	for _, build := range []func() (inputs.ContractDocument, error){
		func() (inputs.ContractDocument, error) { return inputs.ContractDocument{}, errors.New("schema fault") },
		func() (inputs.ContractDocument, error) {
			return inputs.ContractDocument{Defs: map[string]*inputs.Schema{"bad": {AdditionalProperties: make(chan int)}}}, nil
		},
	} {
		if _, err := encodeContract(build); err == nil {
			t.Fatal("invalid contract published")
		}
	}
}
