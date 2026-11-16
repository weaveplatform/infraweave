package urls_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/go-chi/chi/v5"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/gen/api"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
	"github.com/weaveplatform/infraweave/internal/state/internal/core"
	"github.com/weaveplatform/infraweave/internal/state/internal/signed"
	"github.com/weaveplatform/infraweave/internal/state/internal/urls"
)

var errDown = errors.New("down")

// keys derive one fixed key per purpose, or fail.
type keys struct{ fail bool }

func (k keys) DeriveKey(purpose string) ([]byte, uint32, error) {
	if k.fail {
		return nil, 0, errDown
	}
	return []byte(purpose), 1, nil
}

func (keys) DerivedKey(purpose string, _ uint32) ([]byte, error) { return []byte(purpose), nil }

// world is a fake core, stacks domain and authorizer: one stack, slug
// infra, its settings, whether the caller may read its state, and its
// current version. err names the one method that fails with errDown.
type world struct {
	settings stacksapi.Settings
	read     bool
	current  *stateapi.Version
	body     string
	uploaded []string
	upload   error
	gone     bool
	err      string
}

func (w *world) failing(m string) error {
	if w.err == m {
		return errDown
	}
	return nil
}

func (w *world) BySlug(_ context.Context, _ kernel.AccountID, slug string) (stacksapi.Stack, error) {
	if slug != w.settings.Slug {
		return stacksapi.Stack{}, stacksapi.ErrNotFound
	}
	return w.settings.Stack, w.failing("BySlug")
}

func (w *world) Settings(_ context.Context, _ kernel.AccountID, id kernel.ULID) (stacksapi.Settings, error) {
	if id != w.settings.ID {
		return stacksapi.Settings{}, stacksapi.ErrNotFound
	}
	return w.settings, w.failing("Settings")
}

func (w *world) Authorize(_ context.Context, _ kernel.Principal, a accessapi.Action, space kernel.ULID) (bool, error) {
	return w.read && a == accessapi.ActionStackStateRead && space == w.settings.Space,
		w.failing("Authorize")
}

func (w *world) Latest(_ context.Context, _ kernel.AccountID, _ kernel.ULID, workspace string) (stateapi.Version, bool, error) {
	if w.current == nil || workspace != stateapi.DefaultWorkspace {
		return stateapi.Version{}, false, w.failing("Latest")
	}
	return *w.current, true, w.failing("Latest")
}

func (w *world) Upload(_ context.Context, acct kernel.AccountID, object kernel.ULID, subject string, body io.Reader, size int64) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return errors.Join(errDown, err)
	}
	if w.upload != nil {
		return w.upload
	}
	w.uploaded = append(w.uploaded, strings.Join(
		[]string{acct.String(), object.String(), subject, string(data)}, " "))
	if int64(len(data)) != size {
		return errDown
	}
	return nil
}

func (w *world) Download(_ context.Context, _ kernel.AccountID, stack, version kernel.ULID) (core.Version, io.ReadCloser, error) {
	switch {
	case w.err == "Download":
		return core.Version{}, nil, errDown
	case w.gone || w.current == nil || stack != w.settings.ID || version != w.current.ID:
		return core.Version{}, nil, core.ErrGone
	}
	return core.Version{Version: *w.current}, io.NopCloser(strings.NewReader(w.body)), nil
}

func newWorld() *world {
	w := &world{read: true, body: `{"version":4}`}
	w.settings.ID, w.settings.Slug, w.settings.Space = kernel.NewULID(), "infra", kernel.NewULID()
	w.settings.ManagesState, w.settings.ExternalStateAccess = true, true
	w.current = &stateapi.Version{ID: kernel.NewULID(), Stack: w.settings.ID, Size: 13}
	return w
}

const public = "https://infraweave.example/"

func server(w *world, k keys) (urls.Server, http.Handler) {
	s := urls.New(urls.Deps{
		State: w, Stacks: w, Authz: w, Signer: signed.New(k, time.Now),
		Public: public, TTL: time.Minute,
	})
	r := chi.NewRouter()
	s.Routes(r)
	return s, r
}

var who = kernel.Principal{
	Kind: kernel.PrincipalUser, ActorID: kernel.NewULID(), AccountID: kernel.NewAccountID(),
}

// status is the status of the problem err answers, 0 for none.
func status(err error) int {
	if pe, ok := errors.AsType[*problem.Error](err); ok {
		return pe.Problem.Status
	}
	return 0
}

// tamper changes a character inside url's token, whose every bit counts.
func tamper(url string) string {
	i := len(url) - 10
	c := byte('A')
	if url[i] == c {
		c = 'B'
	}
	return url[:i] + string(c) + url[i+1:]
}

// call sends method to url's path through h, declaring size bytes of
// body, -1 for no Content-Length.
func call(t *testing.T, h http.Handler, method, url string, body io.Reader, size int64) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method,
		strings.TrimPrefix(url, strings.TrimSuffix(public, "/")), body)
	req.ContentLength = size
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestUnitUpload(t *testing.T) {
	w := newWorld()
	s, h := server(w, keys{})
	ctx := kernel.WithPrincipal(t.Context(), who)
	if _, err := s.StateUploadUrl(t.Context(), api.StateUploadUrlRequestObject{}); status(err) !=
		http.StatusUnauthorized {
		t.Errorf("stateUploadUrl with no principal = %v", err)
	}
	res, err := s.StateUploadUrl(ctx, api.StateUploadUrlRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	u := res.(api.StateUploadUrl200JSONResponse).Body
	if !strings.HasPrefix(u.Url, "https://infraweave.example"+stateapi.BaseURL+"/uploads/") ||
		u.UploadHeaders == nil || len(u.UploadHeaders.Entries) != 0 {
		t.Fatalf("stateUploadUrl = %+v", u)
	}
	for _, c := range []struct {
		name string
		url  string
		body io.Reader
		size int64
		want int
	}{
		{"no Content-Length", u.Url, strings.NewReader("{}"), -1, http.StatusBadRequest},
		{"over 100 MiB", u.Url, strings.NewReader("{}"), core.MaxUpload + 1, http.StatusRequestEntityTooLarge},
		{"a bad token", u.Url + "x", strings.NewReader("{}"), 2, http.StatusForbidden},
		{"a tampered token", tamper(u.Url), strings.NewReader("{}"), 2, http.StatusForbidden},
		{"a body that fails", u.Url, iotest.ErrReader(errDown), 2, http.StatusBadRequest},
		{"the upload", u.Url, strings.NewReader("{}"), 2, http.StatusOK},
	} {
		if rec := call(t, h, http.MethodPut, c.url, c.body, c.size); rec.Code != c.want {
			t.Errorf("%s = %d %s", c.name, rec.Code, rec.Body)
		}
	}
	want := who.AccountID.String() + " " + u.ObjectId + " " + who.Caller() + " {}"
	if len(w.uploaded) != 1 || w.uploaded[0] != want {
		t.Errorf("uploaded %q, want %q", w.uploaded, want)
	}
	for err, code := range map[error]int{
		core.ErrUploaded: http.StatusConflict, errDown: http.StatusInternalServerError,
	} {
		w.upload = err
		if rec := call(t, h, http.MethodPut, u.Url, strings.NewReader("{}"), 2); rec.Code != code {
			t.Errorf("an upload failing with %v = %d %s", err, rec.Code, rec.Body)
		}
	}
	failing, _ := server(w, keys{fail: true})
	if _, err := failing.StateUploadUrl(ctx, api.StateUploadUrlRequestObject{}); err == nil {
		t.Error("stateUploadUrl signed without keys")
	}
}

func TestUnitDownloadUrl(t *testing.T) {
	ctx := kernel.WithPrincipal(t.Context(), who)
	req := api.StateDownloadUrlRequestObject{StackId: "infra"}
	w := newWorld()
	s, h := server(w, keys{})
	if _, err := s.StateDownloadUrl(t.Context(), req); status(err) != http.StatusUnauthorized {
		t.Errorf("stateDownloadUrl with no principal = %v", err)
	}
	res, err := s.StateDownloadUrl(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	url := res.(api.StateDownloadUrl200JSONResponse).Body.Url
	if !strings.HasPrefix(url, "https://infraweave.example"+stateapi.BaseURL+"/download/") {
		t.Fatalf("stateDownloadUrl = %s", url)
	}
	rec := call(t, h, http.MethodGet, url, nil, 0)
	if rec.Code != http.StatusOK || rec.Body.String() != w.body ||
		rec.Header().Get("Content-Length") != "13" ||
		rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("a download = %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	if rec := call(t, h, http.MethodPut, strings.Replace(url, "/download/", "/uploads/", 1),
		strings.NewReader("{}"), 2); rec.Code != http.StatusForbidden {
		t.Errorf("a download token used to upload = %d %s", rec.Code, rec.Body)
	}

	versioned := req
	versioned.Body = &api.StateDownloadUrlJSONRequestBody{Version: new("1")}
	if _, err := s.StateDownloadUrl(ctx, versioned); err == nil ||
		!errors.As(err, new(*kernel.Invalid)) {
		t.Errorf("a named version = %v", err)
	}
	for _, c := range []struct {
		name string
		set  func(*world)
		want int
		down bool
	}{
		{"an unknown stack", func(w *world) { w.settings.Slug = "other" }, http.StatusNotFound, false},
		{"no managed state", func(w *world) { w.settings.ManagesState = false }, http.StatusNotFound, false},
		{"no STACK_STATE_READ", func(w *world) { w.read = false }, http.StatusForbidden, false},
		{"external access off", func(w *world) { w.settings.ExternalStateAccess = false }, http.StatusForbidden, false},
		{"no state yet", func(w *world) { w.current = nil }, http.StatusNotFound, false},
		{"a failing BySlug", func(w *world) { w.err = "BySlug" }, 0, true},
		{"a failing Authorize", func(w *world) { w.err = "Authorize" }, 0, true},
		{"a failing Latest", func(w *world) { w.err = "Latest" }, 0, true},
	} {
		w := newWorld()
		c.set(w)
		s, _ := server(w, keys{})
		_, err := s.StateDownloadUrl(ctx, req)
		if status(err) != c.want || errors.Is(err, errDown) != c.down {
			t.Errorf("%s = %v", c.name, err)
		}
	}
	w.read = false
	_, err = s.StateDownloadUrl(ctx, req)
	if pe, ok := errors.AsType[*problem.Error](err); !ok ||
		pe.Problem.Action != string(accessapi.ActionStackStateRead) {
		t.Errorf("a 403 for STACK_STATE_READ = %v", err)
	}
	failing, _ := server(newWorld(), keys{fail: true})
	if _, err := failing.StateDownloadUrl(ctx, api.StateDownloadUrlRequestObject{
		StackId: "infra",
	}); err == nil {
		t.Error("stateDownloadUrl signed without keys")
	}
}

func TestUnitDownload(t *testing.T) {
	ctx := kernel.WithPrincipal(t.Context(), who)
	for _, c := range []struct {
		name string
		set  func(*world)
		url  func(string) string
		want int
	}{
		{"a bad token", nil, func(u string) string { return u + "x" }, http.StatusForbidden},
		{"a deleted stack", func(w *world) { w.settings.ID = kernel.NewULID() }, nil, http.StatusNotFound},
		{"no managed state", func(w *world) { w.settings.ManagesState = false }, nil, http.StatusNotFound},
		{"external access off", func(w *world) { w.settings.ExternalStateAccess = false }, nil, http.StatusForbidden},
		{"a gone version", func(w *world) { w.gone = true }, nil, http.StatusNotFound},
		{"a failing stack read", func(w *world) { w.err = "Settings" }, nil, http.StatusInternalServerError},
		{"a failing store", func(w *world) { w.err = "Download" }, nil, http.StatusInternalServerError},
	} {
		w := newWorld()
		s, h := server(w, keys{})
		res, err := s.StateDownloadUrl(ctx, api.StateDownloadUrlRequestObject{StackId: "infra"})
		if err != nil {
			t.Fatal(err)
		}
		url := res.(api.StateDownloadUrl200JSONResponse).Body.Url
		if c.set != nil {
			c.set(w)
		}
		if c.url != nil {
			url = c.url(url)
		}
		if rec := call(t, h, http.MethodGet, url, nil, 0); rec.Code != c.want {
			t.Errorf("%s = %d %s", c.name, rec.Code, rec.Body)
		}
	}
}

func TestUnitUnserved(t *testing.T) {
	r := chi.NewRouter()
	urls.Unserved(r)
	for _, method := range []string{http.MethodPut, http.MethodGet} {
		target := map[string]string{
			http.MethodPut: stateapi.BaseURL + "/uploads/x",
			http.MethodGet: stateapi.BaseURL + "/download/x",
		}[method]
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, target,
			bytes.NewReader(nil)))
		if rec.Code != http.StatusNotImplemented {
			t.Errorf("%s %s = %d", method, target, rec.Code)
		}
	}
}
