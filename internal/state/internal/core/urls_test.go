package core

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
)

// TestUnitUpload checks that an upload is stored under its object and
// subject in the uploads store, live once whole, that a second one
// answers ErrUploaded, and that another subject's lands elsewhere.
func TestUnitUpload(t *testing.T) {
	acct, object := kernel.NewAccountID(), kernel.NewULID()
	b := newBucket()
	s := New(Deps{DB: db{}, Store: newMem(), Objects: b})
	body := []byte(`{"version": 4}`)
	if err := s.Upload(t.Context(), acct, object, "user:a", bytes.NewReader(body),
		int64(len(body))); err != nil {
		t.Fatal(err)
	}
	k := string(objectstore.Uploads) + "/" + UploadKey(acct, object, "user:a")
	if b.states[k] != "live" || !bytes.Equal(b.objects[k], body) {
		t.Errorf("the upload is %q under %s", b.states[k], k)
	}
	if !strings.HasPrefix(UploadKey(acct, object, "user:a"), acct.String()+"/state/"+
		object.String()+"/") || UploadKey(acct, object, "user:b") == UploadKey(acct, object, "user:a") {
		t.Error("an upload's key does not name its account, object and subject")
	}
	err := s.Upload(t.Context(), acct, object, "user:a", bytes.NewReader(body), int64(len(body)))
	if !errors.Is(err, ErrUploaded) {
		t.Errorf("a second upload = %v", err)
	}
	if err := s.Upload(t.Context(), acct, object, "user:b", bytes.NewReader(body),
		int64(len(body))); err != nil {
		t.Errorf("another subject's upload = %v", err)
	}
	b.broken = "Put"
	if err := s.Upload(t.Context(), acct, kernel.NewULID(), "user:a", bytes.NewReader(body),
		int64(len(body))); !errors.Is(err, errDown) {
		t.Errorf("a failing store = %v", err)
	}
	b.broken = "Commit"
	if err := s.Upload(t.Context(), acct, kernel.NewULID(), "user:a", bytes.NewReader(body),
		int64(len(body))); !errors.Is(err, errDown) {
		t.Errorf("a failing commit = %v", err)
	}
}

// TestUnitDownload checks that a download opens its version of its
// stack, and answers ErrGone for a version that is gone, another stack's,
// or one whose object is.
func TestUnitDownload(t *testing.T) {
	acct, stack := kernel.NewAccountID(), kernel.NewULID()
	m, b := newMem(), newBucket()
	s := New(Deps{DB: db{}, Store: m, Objects: b})
	v := Version{Version: stateapi.Version{ID: kernel.NewULID(), Stack: stack}, Key: "k"}
	if err := m.AddVersion(t.Context(), acct, v); err != nil {
		t.Fatal(err)
	}
	b.objects["states/k"], b.states["states/k"] = []byte("state"), "live"
	got, r, err := s.Download(t.Context(), acct, stack, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(r)
	r.Close()
	if got.ID != v.ID || string(data) != "state" {
		t.Errorf("Download = %+v, %q", got, data)
	}
	for name, c := range map[string]struct{ stack, version kernel.ULID }{
		"an unknown version": {stack, kernel.NewULID()},
		"another stack's":    {kernel.NewULID(), v.ID},
	} {
		if _, _, err := s.Download(t.Context(), acct, c.stack, c.version); !errors.Is(err, ErrGone) {
			t.Errorf("%s = %v", name, err)
		}
	}
	b.states["states/k"] = "deleting"
	if _, _, err := s.Download(t.Context(), acct, stack, v.ID); !errors.Is(err, ErrGone) {
		t.Errorf("a deleted object = %v", err)
	}
	b.broken = "Open"
	if _, _, err := s.Download(t.Context(), acct, stack, v.ID); !errors.Is(err, errDown) {
		t.Errorf("a failing object store = %v", err)
	}
	m.fail = "Version"
	if _, _, err := s.Download(t.Context(), acct, stack, v.ID); !errors.Is(err, errDown) {
		t.Errorf("a failing store = %v", err)
	}
}
