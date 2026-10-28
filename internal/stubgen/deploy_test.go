package stubgen_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/weaveplatform/infraweave/internal/server/gen/deploy"
	"github.com/weaveplatform/infraweave/internal/server/problem"
)

// TestUnitDeployStubAnswers501 mounts the generated deploy handler on the
// generated stubs and checks that a scaffolded route answers the 501
// problem the stub promises.
func TestUnitDeployStubAnswers501(t *testing.T) {
	strict := deploy.NewStrictHandlerWithOptions(
		deploy.UnimplementedStrict{},
		nil,
		deploy.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  problem.RequestError,
			ResponseErrorHandlerFunc: problem.ResponseError,
		},
	)
	srv := httptest.NewServer(deploy.HandlerWithOptions(strict, deploy.ChiServerOptions{
		BaseURL:          "/api/v1",
		ErrorHandlerFunc: problem.RequestError,
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/stacks")
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
