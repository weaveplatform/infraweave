package stubgen_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// TestUnitAPIStubAnswers501 mounts the generated API handler on the
// generated stubs and checks that a scaffolded route answers the 501
// problem the stub promises.
func TestUnitAPIStubAnswers501(t *testing.T) {
	strict := api.NewStrictHandlerWithOptions(
		api.UnimplementedStrict{},
		nil,
		api.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  problem.RequestError,
			ResponseErrorHandlerFunc: problem.ResponseError,
		},
	)
	srv := httptest.NewServer(api.HandlerWithOptions(strict, api.ChiServerOptions{
		BaseURL:          "/api/v1",
		ErrorHandlerFunc: problem.RequestError,
	}))
	defer srv.Close()

	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		srv.URL+"/api/v1/stacks",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented ||
		resp.Header.Get("Content-Type") != problem.ContentType {
		t.Fatalf("GET /api/v1/stacks = %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var p problem.Problem
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	if p.Status != http.StatusNotImplemented ||
		p.Detail != "stacks (GET /stacks) is not implemented yet" {
		t.Errorf("problem = %+v", p)
	}
}
