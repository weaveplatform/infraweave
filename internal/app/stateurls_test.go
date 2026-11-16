package app

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
)

// signedCall sends method to the path of url, a signed state URL, with
// no credential, as a client of the URL does; size is the Content-Length
// it declares, -1 for none.
func signedCall(t *testing.T, h http.Handler, method, url string, body []byte, size int64) *httptest.ResponseRecorder {
	t.Helper()
	path, ok := strings.CutPrefix(url, statePublic)
	if !ok {
		t.Fatalf("%s is not under %s", url, statePublic)
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, bytes.NewReader(body))
	req.ContentLength = size
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestAccStateURLs checks the signed state URLs as the server wires the
// access, runs, stacks and state domains over PostgreSQL and an fs
// bucket. An upload URL's PUT, which takes no credential, stores its file
// sealed in uploads under the object and the principal that asked, once:
// a second PUT answers 409, a tampered or other-kind token 403, a body
// without Content-Length 400 and one over 100 MiB 413. A download URL
// reads the stack's current version while the stack keeps its state with
// external state access on: 403 once access is off, 404 once the stack
// no longer keeps its state or the version is gone. stateDownloadUrl needs
// STACK_STATE_READ beside its route's action, refuses a version and
// answers 404 for a stack without state.
func TestAccStateURLs(t *testing.T) {
	w := newClaimWorld(t)
	token := applying(t, w)
	s := newStateServer(t, w, objectstore.DriverFS)
	partial := s.state.API()

	up, err := partial.StateUploadUrl(w.admin, api.StateUploadUrlRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	u := up.(api.StateUploadUrl200JSONResponse).Body
	object, err := kernel.ParseULID(u.ObjectId)
	if err != nil || !strings.HasPrefix(u.Url, statePublic+stateapi.BaseURL+"/uploads/") ||
		u.UploadHeaders == nil || len(u.UploadHeaders.Entries) != 0 {
		t.Fatalf("stateUploadUrl = %+v, %v", u, err)
	}
	secret := "s3cr3t-" + kernel.NewULID().String()
	file := []byte(secretState(7, secret))
	if rec := signedCall(t, s.h, http.MethodPut, u.Url, file, -1); rec.Code != http.StatusBadRequest {
		t.Errorf("an upload without Content-Length = %d %s", rec.Code, rec.Body)
	}
	if rec := signedCall(t, s.h, http.MethodPut, u.Url, file,
		100<<20+1); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("an upload over 100 MiB = %d %s", rec.Code, rec.Body)
	}
	if rec := signedCall(t, s.h, http.MethodPut, u.Url, file,
		int64(len(file))); rec.Code != http.StatusOK {
		t.Fatalf("an upload = %d %s", rec.Code, rec.Body)
	}
	var (
		key, state string
		sealed     []byte
	)
	if err := w.q.QueryRow(t.Context(), `SELECT key, state, content_key_sealed FROM blob_objects
		WHERE store = 'uploads' AND starts_with(key, $1)`,
		w.acct.String()+"/state/"+object.String()+"/").Scan(&key, &state, &sealed); err != nil {
		t.Fatal(err)
	}
	if state != "live" || sealed == nil {
		t.Errorf("the upload's object is %s, content key %x", state, sealed)
	}
	r, err := s.store.Get(t.Context(), objectstore.Uploads, key)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := io.ReadAll(r)
	r.Close()
	if bytes.Contains(stored, []byte(secret)) || bytes.Contains(stored, []byte("5e1d")) {
		t.Error("the uploads store holds the state's plaintext")
	}
	if rec := signedCall(t, s.h, http.MethodPut, u.Url, file,
		int64(len(file))); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), "invalid-state") {
		t.Errorf("a second upload = %d %s", rec.Code, rec.Body)
	}
	i := len(u.Url) - 10
	tampered := u.Url[:i] + map[bool]string{true: "A", false: "B"}[u.Url[i] != 'A'] + u.Url[i+1:]
	if rec := signedCall(t, s.h, http.MethodPut, tampered, file,
		int64(len(file))); rec.Code != http.StatusForbidden {
		t.Errorf("a tampered upload token = %d %s", rec.Code, rec.Body)
	}

	// The stack's state is written through the backend, then read through
	// a download URL.
	c := stateCalls{h: s.h, token: token}
	ws := stateapi.BaseURL + "/stacks/" + w.stacks["one"].String()
	if rec := c.send(t, stateapi.MethodLock, ws, lockInfo("urls")); rec.Code != http.StatusOK {
		t.Fatalf("LOCK = %d %s", rec.Code, rec.Body)
	}
	download := func() (string, error) {
		t.Helper()
		got, err := partial.StateDownloadUrl(w.admin, api.StateDownloadUrlRequestObject{
			StackId: "one", Body: &api.StateDownloadUrlJSONRequestBody{},
		})
		if err != nil {
			return "", err
		}
		return got.(api.StateDownloadUrl200JSONResponse).Body.Url, nil
	}
	w.exec(t, `UPDATE stacks SET vendor_config = '{"externalStateAccessEnabled": true}'
		WHERE id = $1`, w.stacks["one"])
	if _, err := download(); status(err) != http.StatusNotFound {
		t.Errorf("a download URL of a stack with no state = %v", err)
	}
	current := secretState(1, secret)
	if rec := c.post(t, ws+"?ID=urls", current, ""); rec.Code != http.StatusOK {
		t.Fatalf("a write = %d %s", rec.Code, rec.Body)
	}
	url, err := download()
	if err != nil || !strings.HasPrefix(url, statePublic+stateapi.BaseURL+"/download/") {
		t.Fatalf("stateDownloadUrl = %q, %v", url, err)
	}
	if rec := signedCall(t, s.h, http.MethodGet, url, nil, 0); rec.Code != http.StatusOK ||
		rec.Body.String() != current {
		t.Errorf("a download = %d %s", rec.Code, rec.Body)
	}
	if rec := signedCall(t, s.h, http.MethodPut, strings.Replace(url, "/download/",
		"/uploads/", 1), file, int64(len(file))); rec.Code != http.StatusForbidden {
		t.Errorf("a download token used to upload = %d %s", rec.Code, rec.Body)
	}

	_, err = partial.StateDownloadUrl(w.admin, api.StateDownloadUrlRequestObject{
		StackId: "one", Body: &api.StateDownloadUrlJSONRequestBody{Version: new("01")},
	})
	if inv, ok := errors.AsType[*kernel.Invalid](err); !ok || inv.Errors[0].Pointer != "/version" {
		t.Errorf("a download URL of a named version = %v", err)
	}
	stranger := kernel.WithPrincipal(t.Context(), kernel.Principal{
		Kind: kernel.PrincipalUser, ActorID: kernel.NewULID(), AccountID: w.acct,
		MemberID: kernel.NewMemberID(),
	})
	_, err = partial.StateDownloadUrl(stranger, api.StateDownloadUrlRequestObject{StackId: "one"})
	if pe, ok := errors.AsType[*problem.Error](err); !ok || pe.Problem.Status != http.StatusForbidden ||
		pe.Problem.Action != "STACK_STATE_READ" {
		t.Errorf("a download URL without STACK_STATE_READ = %v", err)
	}

	w.exec(t, `UPDATE stacks SET vendor_config = '{}' WHERE id = $1`, w.stacks["one"])
	if rec := signedCall(t, s.h, http.MethodGet, url, nil, 0); rec.Code != http.StatusForbidden {
		t.Errorf("a download with external state access off = %d %s", rec.Code, rec.Body)
	}
	if _, err := download(); status(err) != http.StatusForbidden {
		t.Errorf("a download URL with external state access off = %v", err)
	}
	w.exec(t, `UPDATE stacks SET vendor_config = '{"externalStateAccessEnabled": true}'
		WHERE id = $1`, w.stacks["one"])
	w.exec(t, `DELETE FROM managed_state_versions WHERE account_id = $1 AND stack_id = $2`,
		w.acct, w.stacks["one"])
	if rec := signedCall(t, s.h, http.MethodGet, url, nil, 0); rec.Code != http.StatusNotFound {
		t.Errorf("a download of a deleted version = %d %s", rec.Code, rec.Body)
	}
	w.exec(t, `UPDATE stacks SET manages_state = false WHERE id = $1`, w.stacks["one"])
	if rec := signedCall(t, s.h, http.MethodGet, url, nil, 0); rec.Code != http.StatusNotFound {
		t.Errorf("a download from a stack without managed state = %d %s", rec.Code, rec.Body)
	}
	if _, err := download(); status(err) != http.StatusNotFound {
		t.Errorf("a download URL of a stack without managed state = %v", err)
	}
}

// status is the status of the problem err answers, 0 for none.
func status(err error) int {
	if pe, ok := errors.AsType[*problem.Error](err); ok {
		return pe.Problem.Status
	}
	return 0
}
