package harness

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/test/deps"
)

// mirror is a tool mirror as launcher.tool_mirror names one, which serves
// the files of each release added to it at /<tool>/<version>/<file>.
type mirror struct {
	srv *httptest.Server
	// files are the paths of the files served, by their URL path.
	files map[string]string
}

// Mirror returns the URL of the test binary's tool mirror, started on
// first use, once it serves t's release for the daemon's architecture
// from the tool cache. A launcher downloads and verifies a run's tool from
// it as it would from the vendor, against the vendor's signing key, so
// the suite fetches each release from the internet once, not each
// launcher's cache.
func (l *Launchers) Mirror(ctx context.Context, d *deps.Dependencies, t deps.Tool) (string, error) {
	files, err := d.Release(ctx, t, l.Arch)
	if err != nil {
		return "", err //nolint:wrapcheck // names the release
	}
	l.mirrorMu.Lock()
	defer l.mirrorMu.Unlock()
	if l.mirror == nil {
		m := &mirror{files: map[string]string{}}
		r := chi.NewRouter()
		r.Get("/{tool}/{version}/{file}", func(w http.ResponseWriter, r *http.Request) {
			l.mirrorMu.Lock()
			path, ok := m.files[r.URL.Path]
			l.mirrorMu.Unlock()
			if !ok {
				http.NotFound(w, r)
				return
			}
			http.ServeFile(w, r, path)
		})
		m.srv = httptest.NewServer(r)
		l.mirror = m
	}
	for name, path := range files {
		l.mirror.files["/"+t.Name+"/"+t.Version+"/"+name] = path
	}
	return l.mirror.srv.URL, nil
}

// TamperedMirror is a tool mirror that serves what the mirror at upstream
// serves, but for t's build for arch, which it serves with a byte added,
// as a download altered on its way arrives. A launcher whose mirror it is
// finds the build's SHA-256 is not the one the signed SHA256SUMS lists.
// Its caller closes it.
func TamperedMirror(upstream string, t deps.Tool, arch string) *httptest.Server {
	tampered := "/" + t.Name + "/" + t.Version + "/" + t.Archive(arch)
	r := chi.NewRouter()
	r.Get("/{tool}/{version}/{file}", func(w http.ResponseWriter, r *http.Request) {
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstream+r.URL.Path,
			nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		if r.URL.Path == tampered && resp.StatusCode == http.StatusOK {
			_, _ = w.Write([]byte{0})
		}
	})
	return httptest.NewServer(r)
}
