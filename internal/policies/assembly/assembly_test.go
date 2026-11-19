package assembly

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestUnitPublisherRoutes(t *testing.T) {
	for _, tc := range []struct {
		domain *Domain
		status int
	}{{nil, http.StatusNotFound}, {New(), http.StatusOK}} {
		r := chi.NewRouter()
		tc.domain.Routes(r)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, ContractPattern, nil))
		if rec.Code != tc.status {
			t.Fatalf("%d, want %d: %s", rec.Code, tc.status, rec.Body)
		}
	}
}
