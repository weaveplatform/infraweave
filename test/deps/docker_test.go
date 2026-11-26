package deps

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestUnitObjectStoreContaining(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		status  int
		code    string
		wantErr bool
	}{
		{name: "deleted key", status: http.StatusNotFound, code: "NoSuchKey"},
		{name: "untyped not found", status: http.StatusNotFound},
		{name: "typed missing key", status: http.StatusBadRequest, code: "NoSuchKey"},
		{name: "access denied", status: http.StatusForbidden, code: "AccessDenied", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var markerReads atomic.Int32
			r := chi.NewRouter()
			r.Get("/bucket", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/xml")
				_, _ = fmt.Fprint(
					w,
					`<ListBucketResult><IsTruncated>false</IsTruncated><Contents><Key>clean</Key></Contents><Contents><Key>secret</Key></Contents><Contents><Key>logs/</Key></Contents><Contents><Key>removed</Key></Contents></ListBucketResult>`,
				)
			})
			r.Get("/bucket/*", func(w http.ResponseWriter, req *http.Request) {
				switch req.URL.Path {
				case "/bucket/clean":
					_, _ = fmt.Fprint(w, "encrypted")
				case "/bucket/secret":
					_, _ = fmt.Fprint(w, "contains needle")
				case "/bucket/logs/":
					markerReads.Add(1)
					http.Error(w, "directory marker must not be read", http.StatusForbidden)
				case "/bucket/removed":
					w.Header().Set("Content-Type", "application/xml")
					w.WriteHeader(tc.status)
					if tc.code != "" {
						_, _ = fmt.Fprintf(w, "<Error><Code>%s</Code></Error>", tc.code)
					}
				default:
					http.NotFound(w, req)
				}
			})
			server := httptest.NewServer(r)
			t.Cleanup(server.Close)
			store := ObjectStore{Endpoint: server.URL, AccessKey: "key", SecretKey: "secret"}
			keys, n, err := store.Containing(t.Context(), "bucket", []byte("needle"))
			if (err != nil) != tc.wantErr {
				t.Fatalf("Containing = %v, %d, %v; want error %v", keys, n, err, tc.wantErr)
			}
			if !tc.wantErr && (n != 2 || !slices.Equal(keys, []string{"secret"})) {
				t.Fatalf("Containing = %v, %d; want [secret], 2", keys, n)
			}
			if markerReads.Load() != 0 {
				t.Fatal("read a directory marker")
			}
		})
	}
}
