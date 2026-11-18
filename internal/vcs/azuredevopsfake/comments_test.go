package azuredevopsfake_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/vcs/azuredevopsfake"
)

func TestUnitCommentNativeRefusals(t *testing.T) {
	t.Parallel()
	f := azuredevopsfake.New()
	token := f.AddToken("writer")
	f.Grant("Platform", "writer", azuredevopsfake.Contributor)
	f.Repo("Platform", "infra")
	h := f.Handler()
	send := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
		req.SetBasicAuth("", token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	base := "/acme/Platform/_apis/git/repositories/infra/pullRequests/7/threads"
	for _, body := range []string{"{", "{}"} {
		if w := send("POST", base, body); w.Code != 400 {
			t.Fatal("invalid native create", w.Code, w.Body.String())
		}
	}
	if w := send("POST", base, `{"comments":[{"content":"native thread","parentCommentId":0,"commentType":1}],"status":1}`); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	records := f.Comments("Platform", "infra", "7")
	if len(records) != 1 {
		t.Fatal(records)
	}
	identity := strconv.FormatInt(records[0].ID, 10)
	suffix := "/" + strconv.FormatInt(records[0].ThreadID, 10) + "/comments/" + identity
	for _, body := range []string{"{", "{}"} {
		if w := send("PATCH", base+suffix, body); w.Code != 400 {
			t.Fatal("invalid native update", w.Code, w.Body.String())
		}
	}
	if w := send("PATCH", base+suffix, `{"content":"changed"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send(http.MethodGet, base+"/999999"+"/comments/1", ""); w.Code != 404 {
		t.Fatal("missing native identity", w.Code, w.Body.String())
	}
	limited := f.AddToken("writer", azuredevopsfake.ScopeCode)
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		base,
		strings.NewReader(`{"comments":[{"content":"native thread","parentCommentId":0,"commentType":1}],"status":1}`),
	)
	req.SetBasicAuth("", limited)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("read-only PAT wrote a thread", w.Code)
	}
}
