package gitmem

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
)

func TestUnitUploadBarrierReleaseAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "release", true: "cancel"}[cancelled], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				repo := New()
				repo.Commit("main", "first", map[string]string{"main.tf": "first"})
				repo.HoldUploadPacks()
				repo.HoldUploadPacks()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				done := make(chan struct{})
				w := httptest.NewRecorder()
				go func() {
					defer close(done)
					r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/infra.git/git-upload-pack", strings.NewReader("broken"))
					repo.ServeUploadPack(w, r)
				}()
				synctest.Wait()
				if repo.HeldUploadPacks() != 1 {
					t.Fatal("upload not blocked")
				}
				// Advertisements and unsupported write requests never join an upload wait.
				refs := httptest.NewRecorder()
				repo.ServeUploadPack(refs, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/infra.git/info/refs?service=git-upload-pack", nil))
				if refs.Code != 200 {
					t.Fatal(refs.Code)
				}
				push := httptest.NewRecorder()
				repo.ServeUploadPack(push, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/infra.git/git-receive-pack", nil))
				if push.Code != 404 || repo.HeldUploadPacks() != 1 {
					t.Fatal("write entered read barrier")
				}
				if cancelled {
					cancel()
				} else {
					repo.ReleaseUploadPacks()
				}
				<-done
				want := http.StatusBadRequest
				if cancelled {
					want = http.StatusRequestTimeout
				}
				if w.Code != want {
					t.Fatal(w.Code, want)
				}
				if repo.HeldUploadPacks() != 0 {
					t.Fatal("finished request retained a hold")
				}
				repo.ReleaseUploadPacks()
				repo.ReleaseUploadPacks()
				next := httptest.NewRecorder()
				repo.ServeUploadPack(next, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/infra.git/git-upload-pack", strings.NewReader("broken")))
				if next.Code != 400 {
					t.Fatal("released barrier blocked future request", next.Code)
				}
			})
		})
	}
}
