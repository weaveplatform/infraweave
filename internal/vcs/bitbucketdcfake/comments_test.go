package bitbucketdcfake_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketdcfake"
)

func TestUnitCommentNativeRefusals(t *testing.T) {
	t.Parallel()
	f := bitbucketdcfake.New()
	token := f.AddToken("writer")
	f.Grant("ACME", "infra", "writer", bitbucketdcfake.Read)
	h := f.Handler()
	send := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	base := "/rest/api/1.0/projects/ACME/repos/infra/pull-requests/7/comments"
	for _, body := range []string{"{", "{}"} {
		if w := send("POST", base, body); w.Code != 400 {
			t.Fatal("invalid native create", w.Code, w.Body.String())
		}
	}
	if w := send("POST", base, `{"text":"native comment"}`); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	records := f.Comments("ACME", "infra", "7")
	if len(records) != 1 {
		t.Fatal(records)
	}
	identity := strconv.FormatInt(records[0].ID, 10)
	suffix := "/" + identity
	for _, body := range []string{"{", "{}"} {
		if w := send("PUT", base+suffix, body); w.Code != 400 {
			t.Fatal("invalid native update", w.Code, w.Body.String())
		}
	}
	if w := send("PUT", base+suffix, `{"version":0,"text":"changed"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send(http.MethodGet, base+"/999999"+"", ""); w.Code != 404 {
		t.Fatal("missing native identity", w.Code, w.Body.String())
	}
	if w := send("PUT", base+suffix, `{"version":0,"text":"stale edit"}`); w.Code != 409 {
		t.Fatal("stale native version accepted", w.Code)
	}
}
