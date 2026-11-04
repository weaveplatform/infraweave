package observe

import (
	"log/slog"
	"net/http"

	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware"
)

// Probes is the admin listener's handler. GET /livez answers 200 while the
// process serves HTTP; decision 0017's readiness and startup probes join it
// when there is something to be ready for.
func Probes(log *slog.Logger) http.Handler {
	r := middleware.NewRouter(log)
	r.Get("/livez", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	return r
}
