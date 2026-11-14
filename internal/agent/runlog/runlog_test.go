package runlog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/agenttest"
	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/platform/mask"
)

// out is the runner's standard output, written by the log's reader and
// read by the test.
type out struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (o *out) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.Write(p) //nolint:wrapcheck // a test double
}

func (o *out) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.String()
}

// lossy is the worker API that loses the answer to the next lose commits
// it made.
type lossy struct {
	*client.Client
	mu   sync.Mutex
	lose int
}

var errLost = errors.New("the answer was lost")

func (l *lossy) LogChunks(ctx context.Context, token, run string, in wire.LogChunksRequest) error {
	if err := l.Client.LogChunks(ctx, token, run, in); err != nil {
		return err //nolint:wrapcheck // a test double
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lose > 0 {
		l.lose--
		return errLost
	}
	return nil
}

// world is a run's log over an in-memory pipe, uploading to agenttest's
// worker API.
type world struct {
	t   *testing.T
	srv *agenttest.Server
	api *lossy
	out *out
	log *Log
	w   io.Writer
}

// newWorld starts the log of run-1, whose secrets are hunter22 and the
// well-known patterns.
func newWorld(t *testing.T) *world {
	t.Helper()
	pool, err := agenttest.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	srv := agenttest.NewServer(pool)
	if err := srv.Assign(agenttest.Run{Assignment: wire.Assignment{
		Run: "run-1", RunToken: "token", State: wire.AssignmentStatePREPARING, StateVersion: 3,
	}}); err != nil {
		t.Fatal(err)
	}
	w := &world{
		t: t, srv: srv, out: &out{},
		api: &lossy{Client: client.New(client.Base(agenttest.Endpoint), srv.Client())},
	}
	r, pw := io.Pipe()
	w.w = pw
	w.log = start(t.Context(), Deps{
		API: w.api, Out: w.out, Log: slog.New(slog.DiscardHandler), Run: "run-1", Token: "token",
		Masker: mask.New([]byte("hunter22")).WellKnown(),
	}, r, pw)
	t.Cleanup(func() { _ = w.log.Close(context.Background()) })
	return w
}

// write writes each part to the pipe, as a step's commands do.
func (w *world) write(parts ...string) {
	w.t.Helper()
	for _, p := range parts {
		if _, err := w.w.Write([]byte(p)); err != nil {
			w.t.Fatal(err)
		}
	}
}

// begin opens state version and fails the test if it cannot.
func (w *world) begin(version int64) {
	w.t.Helper()
	if err := w.log.Begin(w.t.Context(), version); err != nil {
		w.t.Fatal(err)
	}
}

// end ends the open state and fails the test if it cannot.
func (w *world) end() {
	w.t.Helper()
	if err := w.log.End(w.t.Context()); err != nil {
		w.t.Fatal(err)
	}
}

// chunks are the indexed chunks as version/seq: body.
func (w *world) chunks() []string {
	chunks := w.srv.Chunks()
	got := make([]string, 0, len(chunks))
	for _, c := range chunks {
		got = append(got, fmt.Sprintf("%d/%d: %s", c.StateVersion, c.Seq, c.Body))
	}
	return got
}

func equal(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s:\n got %q\nwant %q", what, got, want)
	}
}

// TestUnitLogMasks masks a secret, one split across two writes of a
// line, an ::add-mask word from its own line on and a well-known token,
// leaves a word under 4 bytes visible, and writes each line to standard
// output and the state's chunk alike.
func TestUnitLogMasks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.begin(4)
		jwt := "eyJ" + "hbGciOiJIUzI1NiJ9.eyJ" + "zdWIiOiIxMjM0In0.c2ln"
		w.write("pw hun", "ter22\n", "::add-mask swordfish ab\n", "swordfish and ab\n",
			"  ::add-mask marlin42\nmarlin42 "+jwt+"\n", "::add-masks shark\n", "unterminated")
		w.log.Add([]byte("tuna-env"))
		w.write("\ntuna-env\n")
		w.end()
		want := "pw *****\n::add-mask ***** ab\n***** and ab\n  ::add-mask *****\n***** *****\n" +
			"::add-masks shark\nunterminated\n*****\n"
		if got := w.out.String(); got != want {
			t.Errorf("standard output = %q, want %q", got, want)
		}
		equal(t, "chunks", w.chunks(), []string{"4/0: " + want})
	})
}

// TestUnitLogCuts cuts a chunk before a line that would take it past
// MaxChunk, and a chunk Cut after its first line, so a quiet run's last
// line is indexed that soon with no End; each chunk's times are its
// first and last lines'.
func TestUnitLogCuts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.begin(4)
		line := strings.Repeat("a", mask.MaxLine-1) + "\n"
		for range 17 {
			w.write(line)
		}
		synctest.Wait()
		got := w.srv.Chunks()
		if len(got) != 1 || len(got[0].Body) != MaxChunk || got[0].Seq != 0 {
			t.Fatalf("after 17 lines of 64 KiB: %d chunks", len(got))
		}
		start := time.Now()
		<-time.After(time.Second)
		w.write("quiet\n")
		<-time.After(Cut - time.Second - time.Nanosecond)
		synctest.Wait()
		if n := len(w.srv.Chunks()); n != 1 {
			t.Fatalf("%d chunks before the chunk's time", n)
		}
		<-time.After(time.Nanosecond)
		synctest.Wait()
		got = w.srv.Chunks()
		if len(got) != 2 || got[1].Seq != 1 || len(got[1].Body) != len(line)+len("quiet\n") ||
			got[1].FirstAt != start.Unix() || got[1].LastAt != start.Add(time.Second).Unix() {
			t.Fatalf("after Cut: %+v", got[len(got)-1])
		}
		w.end()
		if n := len(w.srv.Chunks()); n != 2 {
			t.Errorf("End of an empty chunk indexed %d chunks", n)
		}
	})
}

// TestUnitLogRetries sends a chunk again after a failed PUT, a
// declaration and a commit the server could not take for now and a
// commit whose answer was lost, each chunk indexed once; and drops a
// chunk the server refuses, going on with the next.
func TestUnitLogRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.begin(4)
		w.srv.Refuse("put", http.StatusServiceUnavailable, 1)
		w.srv.Refuse("upload-urls", http.StatusTooManyRequests, 1)
		w.srv.Refuse("log-chunks", http.StatusBadGateway, 2)
		w.api.mu.Lock()
		w.api.lose = 1
		w.api.mu.Unlock()
		w.write("one\n")
		// Its retries wait 1, 2, 4, 8 and 16 seconds.
		<-time.After(Cut + 31*time.Second)
		synctest.Wait()
		equal(t, "the first chunk", w.chunks(), []string{"4/0: one\n"})
		w.srv.Refuse("upload-urls", http.StatusUnprocessableEntity, 1)
		w.write("two\n")
		<-time.After(Cut)
		synctest.Wait()
		w.write("three\n")
		w.end()
		equal(t, "chunks", w.chunks(), []string{"4/0: one\n", "4/2: three\n"})
	})
}

// TestUnitLogGivesUp drops a chunk the worker API cannot take within
// retryFor, and one whose declaration answers no URL.
func TestUnitLogGivesUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.begin(4)
		w.srv.Refuse("log-chunks", http.StatusServiceUnavailable, 1000)
		start := time.Now()
		w.write("one\n")
		w.end()
		if took := time.Since(start); took > retryFor || took < retryFor-retryWaitMax {
			t.Errorf("gave up after %s", took)
		}
		w.srv.Refuse("log-chunks", 0, 0)
		w.log.d.API = noURL{w.api}
		w.begin(5)
		w.write("two\n")
		w.end()
		if got := w.chunks(); len(got) != 0 {
			t.Errorf("indexed %q", got)
		}
	})
}

// noURL answers each declaration with no upload URL.
type noURL struct{ API }

func (noURL) UploadURLs(context.Context, string, string, wire.UploadURLsRequest) (
	[]wire.SignedUpload, error,
) {
	return nil, nil
}

// TestUnitLogStates numbers each state version's chunks from 0, and
// writes a line read while no state is open to standard output alone.
func TestUnitLogStates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.begin(4)
		w.write("init\n")
		w.end()
		w.write("between\n")
		w.begin(6)
		w.write("plan\n")
		w.end()
		equal(t, "chunks", w.chunks(), []string{"4/0: init\n", "6/0: plan\n"})
		if got := w.out.String(); got != "init\nbetween\nplan\n" {
			t.Errorf("standard output = %q", got)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := w.log.Begin(ctx, 8); !errors.Is(err, context.Canceled) {
			t.Errorf("Begin after the context ended = %v", err)
		}
	})
}

// TestUnitLogLongLine sees a barrier after a line past mask.MaxLine with
// no newline, and keeps MaxLine bytes of the line.
func TestUnitLogLongLine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.begin(4)
		w.write(strings.Repeat("b", mask.MaxLine+100))
		w.end()
		got := w.srv.Chunks()
		if len(got) != 1 || string(got[0].Body) != strings.Repeat("b", mask.MaxLine)+"\n" {
			t.Errorf("%d chunks", len(got))
		}
	})
}

// TestUnitLogClose ends the reader and the uploader once the last chunk
// is committed; a later End and Write find the log closed, and a Close
// whose context has ended answers its error.
func TestUnitLogClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.begin(4)
		w.write("last\n")
		if err := w.log.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		equal(t, "chunks", w.chunks(), []string{"4/0: last\n"})
		if err := w.log.End(t.Context()); err != nil {
			t.Errorf("End after Close = %v", err)
		}
		if _, err := w.log.Write([]byte("x\n")); err == nil {
			t.Error("Write after Close succeeded")
		}

		w = newWorld(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := w.log.Close(ctx); !errors.Is(err, context.Canceled) {
			t.Errorf("Close with an ended context = %v", err)
		}
	})
}

// TestUnitLogNew logs what is written to the file of an operating
// system pipe, as the session's shell writes it.
func TestUnitLogNew(t *testing.T) {
	pool, err := agenttest.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	srv := agenttest.NewServer(pool)
	if err := srv.Assign(agenttest.Run{Assignment: wire.Assignment{
		Run: "run-1", RunToken: "token", State: wire.AssignmentStatePREPARING, StateVersion: 3,
	}}); err != nil {
		t.Fatal(err)
	}
	o := &out{}
	l, err := New(t.Context(), Deps{
		API: client.New(client.Base(agenttest.Endpoint), srv.Client()), Out: o,
		Log: slog.New(slog.DiscardHandler), Run: "run-1", Token: "token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Begin(t.Context(), 4); err != nil {
		t.Fatal(err)
	}
	if _, err := l.File().WriteString("from the shell\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Write([]byte("from the runner\n")); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := srv.Chunks()
	if len(got) != 1 || string(got[0].Body) != "from the shell\nfrom the runner\n" ||
		o.String() != string(got[0].Body) {
		t.Errorf("chunks %+v, standard output %q", got, o.String())
	}
}

// cancelling is the worker API whose PUT ends the log's context and
// fails.
type cancelling struct {
	API
	cancel context.CancelFunc
}

var errPut = errors.New("the PUT failed")

func (c cancelling) Put(context.Context, wire.SignedUpload, []byte) error {
	c.cancel()
	return errPut
}

// TestUnitLogCanceled stops a chunk's retries when the log's context
// ends, drops a chunk whose send failed as it ended, and writes a line
// still unterminated when the pipe closes to standard output.
func TestUnitLogCanceled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for _, put := range []bool{false, true} {
			ctx, cancel := context.WithCancel(t.Context())
			w := newWorld(t)
			if err := w.log.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			r, pw := io.Pipe()
			w.w = pw
			var api API = w.api
			if put {
				api = cancelling{API: w.api, cancel: cancel}
			}
			w.log = start(ctx, Deps{
				API: api, Out: w.out, Log: slog.New(slog.DiscardHandler), Run: "run-1",
				Token: "token",
			}, r, pw)
			w.begin(4)
			w.srv.Refuse("put", http.StatusServiceUnavailable, 1000)
			w.write("one\n")
			<-time.After(Cut + time.Second/2)
			cancel()
			synctest.Wait()
			if err := w.log.End(t.Context()); err != nil {
				t.Errorf("End after the context ended = %v", err)
			}
			w.write("partial")
			if err := w.log.Close(ctx); !errors.Is(err, context.Canceled) {
				t.Errorf("Close = %v", err)
			}
			synctest.Wait()
			if got := w.out.String(); got != "one\npartial\n" || len(w.srv.Chunks()) != 0 {
				t.Errorf("standard output %q, chunks %+v", got, w.srv.Chunks())
			}
		}
	})
}
