package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/app/config"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware/idempotency"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/fs"
)

// TestUnitBoundedBodies checks server.max_body_bytes on an operation of
// each surface: a body over it answers 413 payload-too-large whether it
// declares its length or arrives chunked, with an Idempotency-Key or
// without, while a body at the bound passes it; and the fs driver's blob
// PUT is bounded by its signed URL, not by it.
func TestUnitBoundedBodies(t *testing.T) {
	c := config.Defaults().Server
	c.MaxBodyBytes = 64
	h := API(discard(), c, memory(c),
		Domains{Auth: byAddress{}, OperatorAuth: byAddress{}, WorkerAuth: byAddress{}},
		Platform{}, Blobs{Routes: (&fs.Bucket{}).Routes})
	sent := 0
	post := func(method, target string, size int, chunked bool, key string) *httptest.ResponseRecorder {
		// Each request is its own caller, so no budget runs out.
		sent++
		body := `{"x":"` + strings.Repeat("x", size-8) + `"}`
		r := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
		r.RemoteAddr = fmt.Sprintf("198.51.100.%d:1", sent)
		r.Header.Set("Content-Type", "application/json")
		if chunked {
			r.ContentLength = -1
		}
		if key != "" {
			r.Header.Set(idempotency.Header, key)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	for _, target := range []string{
		BaseURL + "/auth/token", OperatorBaseURL + "/organizations", WorkerBaseURL + "/register",
	} {
		for _, chunked := range []bool{false, true} {
			for _, key := range []string{"", "k"} {
				t.Run(
					fmt.Sprintf("%s chunked %v key %q", target, chunked, key),
					func(t *testing.T) {
						rec := post(http.MethodPost, target, 65, chunked, key)
						wantProblem(t, rec, http.StatusRequestEntityTooLarge, "at most 64 bytes")
					},
				)
			}
			if rec := post(http.MethodPost, target, 64, chunked, "k"); rec.Code ==
				http.StatusRequestEntityTooLarge {
				t.Errorf("%s, chunked %v: a body at the bound answered %s", target, chunked,
					rec.Body)
			}
		}
	}
	wantTyped(t, post(http.MethodPut, "/blobs/not-a-token", 65, false, ""), problem.Forbidden)
	if op := apiOperations()[http.MethodPut+" "+fs.Pattern]; !op.OwnBodyBound {
		t.Errorf("the blob PUT's operation is %+v", op)
	}
}
