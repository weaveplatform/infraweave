package githubfake_test

import (
	"fmt"
	"net/http"
	"testing"
)

func TestUnitCommentNativePermissions(t *testing.T) {
	t.Parallel()
	f := newDeploymentFixture(t)
	read := f.token(t, `{"pull_requests":"read"}`)
	write := f.token(t, `{"pull_requests":"write"}`)
	path := "/repos/acme/infra/issues/7/comments"
	if w := f.send(t, http.MethodPost, path, read, `{"body":"denied"}`); w.Code != 403 {
		t.Fatal("read permission posted", w.Code)
	}
	for _, body := range []string{"{", "{}"} {
		if w := f.send(t, http.MethodPost, path, write, body); w.Code != 422 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := f.send(t, http.MethodPost, path, write, `{"body":"native comment"}`); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	records := f.f.Comments("acme", "infra", "7")
	if len(records) != 1 {
		t.Fatal(records)
	}
	identity := fmt.Sprintf("/repos/acme/infra/issues/comments/%d", records[0].ID)
	if w := f.send(t, http.MethodGet, identity, read, ""); w.Code != 200 {
		t.Fatal("read permission cannot read", w.Code)
	}
	for _, body := range []string{"{", "{}"} {
		if w := f.send(t, http.MethodPatch, identity, write, body); w.Code != 422 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := f.send(t, http.MethodGet, "/repos/acme/web/issues/comments/"+fmt.Sprint(records[0].ID), write, ""); w.Code != 404 {
		t.Fatal("comment leaked across repository", w.Code)
	}
	if w := f.send(t, http.MethodGet, "/repos/acme/infra/issues/comments/not-a-number", write, ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
}
