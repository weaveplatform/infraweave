package rawgit

import (
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port/sourcetest"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/gitmem"
)

func TestUnitPushDiff(t *testing.T) {
	repo := gitmem.New()
	router := chi.NewRouter()
	router.Handle("/*", http.HandlerFunc(repo.ServeUploadPack))
	server := httptest.NewTLSServer(router)
	t.Cleanup(server.Close)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	sourcetest.Push(t, New(Options{RootCAs: roots}), port.Repository{URL: server.URL + "/infra.git"}, repo)
}
