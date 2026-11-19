package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/policies/internal/inputs"
)

// ContractPattern is the discovery route on the shared API listener.
const ContractPattern = "/.well-known/policy-contract.json"

// Server publishes the schema alone. Authentication, authorization, feature
// gates, rate limits and cache headers belong to the shared listener controls.
type Server struct{ contract func() ([]byte, error) }

// New reflects the contract at most once per domain, reusing the immutable
// schema bytes without caching an authenticated HTTP response.
func New() Server {
	return Server{contract: sync.OnceValues(func() ([]byte, error) { return encodeContract(inputs.Contract) })}
}

func encodeContract(build func() (inputs.ContractDocument, error)) ([]byte, error) {
	c, err := build()
	if err != nil {
		return nil, fmt.Errorf("build policy contract: %w", err)
	}
	b, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("encode policy contract: %w", err)
	}
	return b, nil
}

// Routes registers the root discovery route beside the versioned API.
func (s Server) Routes(r chi.Router) { r.Get(ContractPattern, s.serveContract) }

func (s Server) serveContract(w http.ResponseWriter, r *http.Request) {
	body, err := s.contract()
	if err != nil {
		problem.ResponseError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}
