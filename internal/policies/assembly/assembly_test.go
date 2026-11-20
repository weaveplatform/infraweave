package assembly

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
)

func TestUnitPublisherRoutes(t *testing.T) {
	for _, tc := range []struct {
		domain *Domain
		status int
	}{{nil, http.StatusNotFound}, {New(Deps{}), http.StatusOK}} {
		r := chi.NewRouter()
		tc.domain.Routes(r)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, ContractPattern, nil))
		if rec.Code != tc.status {
			t.Fatalf("%d, want %d: %s", rec.Code, tc.status, rec.Body)
		}
	}
}

func TestUnitDomainAPIPartial(t *testing.T) {
	for _, domain := range []*Domain{nil, New(Deps{})} {
		out, err := domain.API().PolicyRuntime(t.Context(), api.PolicyRuntimeRequestObject{})
		if out == nil && err == nil {
			t.Fatal("runtime metadata produced no response or error")
		}
	}
}

func TestUnitPolicyCheckSurface(t *testing.T) {
	surfaces := New(Deps{}).Surfaces()
	if len(surfaces) != 1 || surfaces[0].Name != "policy" || surfaces[0].CheckPolicy == nil || surfaces[0].Env != nil {
		t.Fatalf("invalid compiler contribution: %+v", surfaces)
	}
}
