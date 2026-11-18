package gitlabfake_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/gitlabfake"
)

func TestUnitCommentNativeRefusals(t *testing.T) {
	t.Parallel()
	f := gitlabfake.New()
	token := f.AddToken("writer", "api")
	f.AddMember("acme", "infra", "writer", gitlabfake.Reporter)
	h := f.Handler()
	send := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
		req.Header.Set("PRIVATE-TOKEN", token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	base := "/api/v4/projects/acme%2Finfra/merge_requests/7/notes"
	for _, body := range []string{"{", "{}"} {
		if w := send("POST", base, body); w.Code != 400 {
			t.Fatal("invalid native create", w.Code, w.Body.String())
		}
	}
	if w := send("POST", base, `{"body":"native note"}`); w.Code != 201 {
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
	if w := send("PUT", base+suffix, `{"body":"changed"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send(http.MethodGet, base+"/999999"+"", ""); w.Code != 404 {
		t.Fatal("missing native identity", w.Code, w.Body.String())
	}
	readonly := f.AddToken("reader", "read_api")
	f.AddMember("acme", "infra", "reader", gitlabfake.Reporter)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, base, strings.NewReader(`{"body":"native note"}`))
	req.Header.Set("PRIVATE-TOKEN", readonly)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("read-only token posted note", w.Code)
	}
}
