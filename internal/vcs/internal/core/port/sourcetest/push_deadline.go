package sourcetest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

// PushHistoryDeadlines exercises the actual bounded adapter when a transport
// reports EOF after its context expires, both before and after response headers.
// The fake clock keeps TLS scheduling out of the error-cause assertion; the
// adapter's separate native HTTP tests still cover the real transport.
func PushHistoryDeadlines(t *testing.T, factory func(http.RoundTripper) port.PushHistory, repo Repository, timeout time.Duration) {
	t.Helper()
	for _, body := range []bool{false, true} {
		name := "request EOF"
		if body {
			name = "body EOF"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				source := factory(deadlineEOFTransport{body: body})
				start := time.Now()
				got, err := source.RecoverPush(t.Context(), repo.Repo, repo.Base, repo.Head)
				if got.Commits != nil || got.Base != "" || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != timeout {
					t.Fatalf("expired native I/O lost its deadline or returned usable facts: range=%+v err=%v elapsed=%v want=%v", got, err, time.Since(start), timeout)
				}
			})
		})
	}
}

type deadlineEOFTransport struct{ body bool }

func (tr deadlineEOFTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if tr.body {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(deadlineEOFReader{ctx: req.Context()})}, nil
	}
	<-req.Context().Done()
	return nil, io.EOF
}

type deadlineEOFReader struct{ ctx context.Context }

func (r deadlineEOFReader) Read([]byte) (int, error) {
	<-r.ctx.Done()
	return 0, io.EOF
}
