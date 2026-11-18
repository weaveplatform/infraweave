package bitbucketcloudfake_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/bitbucketcloudfake"
)

func TestUnitCommentNativeRefusals(t *testing.T) {
	t.Parallel()
	f := bitbucketcloudfake.New()
	token := f.AddToken("writer@example.com")
	f.Grant("acme", "infra", "writer@example.com", bitbucketcloudfake.Read)
	h := f.Handler()
	send := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
		req.SetBasicAuth("writer@example.com", token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	base := "/2.0/repositories/acme/infra/pullrequests/7/comments"
	for _, body := range []string{"{", "{}"} {
		if w := send("POST", base, body); w.Code != 400 {
			t.Fatal("invalid native create", w.Code, w.Body.String())
		}
	}
	if w := send("POST", base, `{"content":{"raw":"native comment"}}`); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	records := f.Comments("acme", "infra", "7")
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
	if w := send("PUT", base+suffix, `{"content":{"raw":"changed"}}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send(http.MethodGet, base+"/999999"+"", ""); w.Code != 404 {
		t.Fatal("missing native identity", w.Code, w.Body.String())
	}
	if w := send(http.MethodGet, base+"?sort=oldest", ""); w.Code != 400 {
		t.Fatal("missing native newest order accepted", w.Code)
	}
}
