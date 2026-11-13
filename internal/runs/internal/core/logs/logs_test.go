package logs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/blobs"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/logs"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/report"
)

var errDown = errors.New("down")

// TestUnitSanitize checks that each kind of escape sequence goes with
// its parameters and terminator, and the rest of the line stays.
func TestUnitSanitize(t *testing.T) {
	for in, want := range map[string]string{
		"plain text":                                           "plain text",
		"\x1b[32mgreen\x1b[0m":                                 "green",
		"\x1b[1;31mbold red\x1b[m!":                            "bold red!",
		"\x1b[?25lhidden cursor\x1b[?25h":                      "hidden cursor",
		"\x1b]8;;https://example.test\x1b\\link\x1b]8;;\x1b\\": "link",
		"\x1b]0;title\x07text":                                 "text",
		"\x1b(Bcharset":                                        "charset",
		"\x1b=keypad":                                          "keypad",
		"\x1bPq#0\x1b\\done":                                   "done",
		"a\x1b]2;never ends":                                   "a",
		"end\x1b":                                              "end",
		"x\x1b[12":                                             "x",
		"héllo \x1b[1m✓\x1b[0m":                                "héllo ✓",
	} {
		if got := logs.Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

// store holds one run, the state it entered at each state version, and
// its chunks by state version; locked, when set, is what LockChunks
// answers; the method named in fail fails.
type store struct {
	run    lifecycle.Run
	states map[int64]kernel.RunState
	chunks map[int64][]report.Chunk
	locked []int64
	fail   string
}

func (s *store) err(method string) error {
	if s.fail == method {
		return fmt.Errorf("%s: %w", method, errDown)
	}
	return nil
}

func (s *store) Get(_ context.Context, acct kernel.AccountID, id kernel.ULID) (lifecycle.Run,
	error,
) {
	if acct != s.run.Account || id != s.run.ID {
		return lifecycle.Run{}, read.ErrNotFound
	}
	return s.run, s.err("Get")
}

func (s *store) Entered(_ context.Context, _ kernel.AccountID, _ kernel.ULID,
	state kernel.RunState,
) (int64, bool, error) {
	var latest int64
	for v, st := range s.states {
		if st == state && v > latest {
			latest = v
		}
	}
	return latest, latest > 0, s.err("Entered")
}

func (s *store) StateAt(_ context.Context, _ kernel.AccountID, _ kernel.ULID,
	version int64,
) (kernel.RunState, error) {
	st, ok := s.states[version]
	if !ok {
		return "", lifecycle.ErrNotFound
	}
	return st, s.err("StateAt")
}

func (s *store) Chunks(_ context.Context, _ kernel.AccountID, _ kernel.ULID, version, from int64,
	limit int,
) ([]report.Chunk, error) {
	var out []report.Chunk
	for _, c := range s.chunks[version] {
		if c.Seq >= from && len(out) < limit {
			out = append(out, c)
		}
	}
	return out, s.err("Chunks")
}

func (s *store) LockChunks(_ context.Context, _ kernel.AccountID, _ kernel.ULID,
	version int64,
) ([]int64, error) {
	if s.locked != nil {
		return s.locked, s.err("LockChunks")
	}
	var out []int64
	for _, c := range s.chunks[version] {
		out = append(out, c.Seq)
	}
	return out, s.err("LockChunks")
}

// objects holds each key's row state, pending (an unfinished write),
// uploaded (a finished one), live or deleting, and the bytes of the
// written ones. The method named in fail fails; failLive fails that
// call of Live, counting from 1.
type objects struct {
	rows     map[string]string
	data     map[string][]byte
	puts     int
	fail     string
	failLive int
	lives    int
	// onLive runs before each call of Live answers, with its count.
	onLive func(n int)
}

func newObjects() *objects {
	return &objects{rows: map[string]string{}, data: map[string][]byte{}}
}

// put stores body under key, live.
func (o *objects) put(key, body string) {
	o.rows[key], o.data[key] = "live", []byte(body)
}

func (o *objects) err(method string) error {
	if o.fail == method {
		return fmt.Errorf("%s: %w", method, errDown)
	}
	return nil
}

func (o *objects) Open(_ context.Context, _ objectstore.Name, key string) (io.ReadCloser,
	error,
) {
	if err := o.err("Open"); err != nil {
		return nil, err
	}
	if o.rows[key] != "live" {
		return nil, objectstore.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(o.data[key])), nil
}

func (o *objects) SignGet(_ context.Context, _ objectstore.Name, key string) (
	objectstore.Signed, error,
) {
	if err := o.err("SignGet"); err != nil {
		return objectstore.Signed{}, err
	}
	if o.rows[key] != "live" {
		return objectstore.Signed{}, objectstore.ErrNotFound
	}
	return objectstore.Signed{URL: "https://store.test/" + key}, nil
}

func (o *objects) Live(_ context.Context, _ objectstore.Name, key string) (bool, error) {
	o.lives++
	if o.onLive != nil {
		o.onLive(o.lives)
	}
	if o.lives == o.failLive {
		return false, errDown
	}
	return o.rows[key] == "live", nil
}

// Put reports a failing read as a driver does, without wrapping it.
func (o *objects) Put(_ context.Context, _ objectstore.Name, key string, r io.Reader,
	size int64,
) (objectstore.Object, error) {
	if err := o.err("Put"); err != nil {
		return objectstore.Object{}, err
	}
	if o.rows[key] != "" {
		return objectstore.Object{}, blobs.ErrKeyInUse
	}
	o.rows[key] = "pending"
	o.puts++
	b, err := io.ReadAll(r)
	if err != nil {
		//nolint:errorlint // a driver may drop the reader's error, which the stream keeps
		return objectstore.Object{}, fmt.Errorf("driver: put %s: %v", key, err)
	}
	if int64(len(b)) != size {
		return objectstore.Object{}, objectstore.ErrSize
	}
	o.rows[key], o.data[key] = "uploaded", b
	return objectstore.Object{Size: size}, nil
}

func (o *objects) Commit(_ context.Context, _ objectstore.Name, key string) error {
	if err := o.err("Commit"); err != nil {
		return err
	}
	if o.rows[key] != "uploaded" {
		return blobs.ErrNotUploaded
	}
	o.rows[key] = "live"
	return nil
}

func (o *objects) Delete(_ context.Context, _ objectstore.Name, key string) error {
	if err := o.err("Delete"); err != nil {
		return err
	}
	if o.rows[key] != "" {
		o.rows[key] = "deleting"
	}
	return nil
}

// jobs counts completions, failing each with err.
type jobs struct {
	completed int
	err       error
}

func (j *jobs) CompleteTx(context.Context, queue.Job) error {
	if j.err != nil {
		return j.err
	}
	j.completed++
	return nil
}

type (
	db     struct{}
	fakeTx struct{ pgx.Tx }
)

func (db) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) { return fakeTx{}, nil }
func (fakeTx) Commit(context.Context) error                       { return nil }
func (fakeTx) Rollback(context.Context) error                     { return nil }

// at is when every chunk's first line was written, in seconds after
// the epoch: chunk seq's is at+seq.
const at = 1_800_000_000

// world is a run of the account acct, in APPLYING at state version 7,
// which entered PLANNING at 5 and APPLYING at 7, with the service over
// it.
type world struct {
	p   kernel.Principal
	s   *store
	o   *objects
	j   *jobs
	svc *logs.Service
}

func newWorld() *world {
	acct := kernel.NewAccountID()
	run := lifecycle.Run{
		Account: acct, ID: kernel.NewULID(), State: kernel.RunStateApplying, Version: 7,
	}
	w := &world{
		p: kernel.Principal{Kind: kernel.PrincipalAPIKey, AccountID: acct},
		s: &store{run: run, states: map[int64]kernel.RunState{
			4: kernel.RunStateInitializing, 5: kernel.RunStatePlanning,
			6: kernel.RunStateConfirmed, 7: kernel.RunStateApplying,
		}, chunks: map[int64][]report.Chunk{}},
		o: newObjects(),
		j: &jobs{},
	}
	w.svc = logs.New(logs.Deps{DB: db{}, Store: w.s, Objects: w.o, Jobs: w.j})
	return w
}

// chunk indexes a chunk seq of state version version holding body, its
// object live unless body is "expired", where retention removed it; bytes
// overrides its indexed size when set.
func (w *world) chunk(version, seq int64, body string, bytes int64) {
	key := fmt.Sprintf("%s/%s/%s-%d/%d", w.s.run.Account, w.s.run.ID, w.s.states[version],
		version, seq)
	if bytes == 0 {
		bytes = int64(len(body))
	}
	if body != "expired" {
		w.o.put(key, body)
	}
	w.s.chunks[version] = append(w.s.chunks[version], report.Chunk{
		Version: version, State: w.s.states[version], Seq: seq, Key: key, Bytes: bytes,
		FirstAt: time.Unix(at+seq, 0), LastAt: time.Unix(at+seq, 0),
	})
	slices.SortFunc(w.s.chunks[version], func(a, b report.Chunk) int {
		return int(a.Seq - b.Seq)
	})
}

func (w *world) page(t *testing.T, q logs.Query) logs.Page {
	t.Helper()
	p, err := w.svc.Page(t.Context(), w.p, w.s.run.ID.String(), q)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// text is p's lines, each as message@seconds-after-at, joined by commas.
func text(p logs.Page) string {
	out := make([]string, len(p.Lines))
	for i, l := range p.Lines {
		out[i] = l.Message + "@" + strconv.FormatInt(l.At.Unix()-at, 10)
	}
	return strings.Join(out, ",")
}

func version(v int64) *int64 { return &v }

// TestUnitLogsPage checks a state's pages: lines in sequence order with
// their chunk's time, a gap waited at while the run is in the state and
// passed once it has left, a removed chunk skipped and marked expired,
// the page's size and chunk limits, sanitizing, the state version named
// or the latest, and a state never entered.
func TestUnitLogsPage(t *testing.T) {
	w := newWorld()
	w.chunk(5, 0, "a\nb\n", 0)
	w.chunk(5, 2, "c", 0)
	w.chunk(5, 3, "expired", 4)
	w.chunk(5, 4, "\x1b[1md\x1b[0m\n", 0)
	w.chunk(5, 5, "", 0)
	got := w.page(t, logs.Query{State: kernel.RunStatePlanning})
	if text(got) != "a@0,b@0,c@2,\x1b[1md\x1b[0m@4" || got.Version != 5 || got.Next != 6 ||
		!got.Exists || got.Ongoing || !got.Expired || got.HasMore || !got.Finished() ||
		got.More() {
		t.Errorf("the left state's page is %q %+v", text(got), got)
	}
	got = w.page(t, logs.Query{
		State: kernel.RunStatePlanning, Version: version(5), From: 4,
		Sanitize: true,
	})
	if text(got) != "d@4" || got.Expired {
		t.Errorf("the sanitized page from 4 is %q %+v", text(got), got)
	}

	w.chunk(7, 0, "x\n", 0)
	w.chunk(7, 2, "z\n", 0)
	got = w.page(t, logs.Query{State: kernel.RunStateApplying})
	if text(got) != "x@0" || got.Next != 1 || !got.Ongoing || got.HasMore || got.Finished() ||
		!got.More() {
		t.Errorf("the ongoing state's page is %q %+v", text(got), got)
	}
	w.chunk(7, 1, "y\n", 0)
	got = w.page(t, logs.Query{State: kernel.RunStateApplying, Version: version(7), From: 1})
	if text(got) != "y@1,z@2" || got.Next != 3 || got.HasMore {
		t.Errorf("the page once the gap is indexed is %q %+v", text(got), got)
	}

	for _, q := range []logs.Query{
		{State: kernel.RunStateDestroying},
		{State: kernel.RunStateApplying, Version: version(5)},
		{State: kernel.RunStatePlanning, Version: version(99)},
	} {
		got = w.page(t, q)
		if got.Exists || !got.Finished() || got.Lines == nil || len(got.Lines) > 0 {
			t.Errorf("%+v answers %+v", q, got)
		}
	}
}

// TestUnitLogsPageLimits checks that a page stops before the chunk that
// would take it past a mebibyte, though it holds one chunk at least, and
// after 64 chunks, with more to read.
func TestUnitLogsPageLimits(t *testing.T) {
	w := newWorld()
	w.s.run.State = kernel.RunStateFinished
	for seq := range int64(3) {
		w.chunk(5, seq, fmt.Sprintf("big %d\n", seq), 700<<10)
	}
	for _, want := range []string{"big 0@0", "big 1@1", "big 2@2"} {
		got := w.page(t, logs.Query{
			State: kernel.RunStatePlanning,
			From:  int64(want[4] - '0'),
		})
		if text(got) != want || got.HasMore != (want != "big 2@2") {
			t.Errorf("page %q is %q %+v", want, text(got), got)
		}
	}
	for seq := range int64(70) {
		w.chunk(4, seq, "l\n", 0)
	}
	got := w.page(t, logs.Query{State: kernel.RunStateInitializing})
	if len(got.Lines) != 64 || !got.HasMore || got.Next != 64 || got.Finished() {
		t.Errorf("the first page holds %d lines: %+v", len(got.Lines), got)
	}
	got = w.page(t, logs.Query{State: kernel.RunStateInitializing, From: got.Next})
	if len(got.Lines) != 6 || got.HasMore || !got.Finished() {
		t.Errorf("the second page holds %d lines: %+v", len(got.Lines), got)
	}
}

// TestUnitLogsPageFailures checks an unknown run and a raw ID that is
// none, and each read's failure.
func TestUnitLogsPageFailures(t *testing.T) {
	w := newWorld()
	w.chunk(5, 0, "a\n", 0)
	for _, raw := range []string{"nope", kernel.NewULID().String()} {
		if _, err := w.svc.Page(t.Context(), w.p, raw,
			logs.Query{State: kernel.RunStatePlanning}); !errors.Is(err, read.ErrNotFound) {
			t.Errorf("Page of run %q: %v", raw, err)
		}
	}
	for _, c := range []struct {
		fail    string
		version *int64
	}{{"Get", nil}, {"Entered", nil}, {"StateAt", version(5)}, {"Chunks", nil}} {
		w.s.fail = c.fail
		if _, err := w.svc.Page(t.Context(), w.p, w.s.run.ID.String(),
			logs.Query{State: kernel.RunStatePlanning, Version: c.version}); !errors.Is(err,
			errDown) {
			t.Errorf("Page with %s failing: %v", c.fail, err)
		}
	}
	w.s.fail, w.o.fail = "", "Open"
	if _, err := w.svc.Page(t.Context(), w.p, w.s.run.ID.String(),
		logs.Query{State: kernel.RunStatePlanning}); !errors.Is(err, errDown) {
		t.Errorf("Page with Open failing: %v", err)
	}
}

// TestUnitLogsDownload checks each answer: a compacted log's URL, ongoing
// while the run is in the state or the compaction is pending, expired for
// a state never entered, with no chunk or whose chunks retention removed,
// and each read's failure.
func TestUnitLogsDownload(t *testing.T) {
	w := newWorld()
	download := func(state kernel.RunState, v *int64) logs.Download {
		t.Helper()
		d, err := w.svc.Download(t.Context(), w.p, w.s.run.ID.String(), state, v)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	planning := report.LogKey(w.s.run.Account, w.s.run.ID, kernel.RunStatePlanning, 5)
	// The cases run in order: compacted writes the object pending reads
	// as missing.
	for _, c := range []struct {
		name  string
		setup func()
		state kernel.RunState
		v     *int64
		want  logs.Download
	}{
		{
			name: "never entered", state: kernel.RunStateDestroying,
			want: logs.Download{Expired: true},
		},
		{
			name: "no chunk", state: kernel.RunStateInitializing,
			want: logs.Download{Expired: true},
		},
		{
			name: "in the state", state: kernel.RunStateApplying,
			want: logs.Download{Ongoing: true},
		},
		{
			name: "pending", setup: func() { w.chunk(5, 0, "a\n", 0) },
			state: kernel.RunStatePlanning, want: logs.Download{Ongoing: true},
		},
		{
			name: "compacted", setup: func() { w.o.put(planning, "a\n") },
			state: kernel.RunStatePlanning, v: version(5),
			want: logs.Download{URL: "https://store.test/" + planning},
		},
		{
			name: "retention removed the chunks", setup: func() { w.chunk(6, 0, "expired", 2) },
			state: kernel.RunStateConfirmed, want: logs.Download{Expired: true},
		},
	} {
		if c.setup != nil {
			c.setup()
		}
		if got := download(c.state, c.v); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
	if _, err := w.svc.Download(t.Context(), w.p, "nope", kernel.RunStatePlanning,
		nil); !errors.Is(err, read.ErrNotFound) {
		t.Errorf("Download of no run: %v", err)
	}
	w.chunk(4, 0, "i\n", 0)
	for _, c := range []struct {
		store, objects string
		live           int
	}{{store: "Entered"}, {objects: "SignGet"}, {store: "Chunks"}, {live: 1}} {
		w.s.fail, w.o.fail, w.o.failLive, w.o.lives = c.store, c.objects, c.live, 0
		if _, err := w.svc.Download(t.Context(), w.p, w.s.run.ID.String(),
			kernel.RunStateInitializing, nil); !errors.Is(err, errDown) {
			t.Errorf("Download with %+v failing: %v", c, err)
		}
	}
}

// compactJob is the job a transition out of state at version enqueues
// for w's run.
func (w *world) compactJob(t *testing.T, state kernel.RunState, version int64) queue.Job {
	t.Helper()
	b, err := json.Marshal(lifecycle.Compact{Run: w.s.run.ID, State: state, StateVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	return queue.Job{Kind: lifecycle.CompactKind.Name, Payload: b, Account: w.s.run.Account.ULID}
}

// TestUnitLogsCompact checks that a state's chunks are written, in order,
// to its log's key and committed with the job, that compacting again
// writes nothing, and that a state with no chunk writes nothing.
func TestUnitLogsCompact(t *testing.T) {
	w := newWorld()
	w.chunk(5, 0, "a\n", 0)
	w.chunk(5, 1, "b\nc\n", 0)
	w.chunk(5, 3, "d\n", 0)
	key := report.LogKey(w.s.run.Account, w.s.run.ID, kernel.RunStatePlanning, 5)
	j := w.compactJob(t, kernel.RunStatePlanning, 5)
	for range 2 {
		if err := w.svc.Compact(t.Context(), j); err != nil {
			t.Fatal(err)
		}
	}
	if w.o.rows[key] != "live" || string(w.o.data[key]) != "a\nb\nc\nd\n" || w.o.puts != 1 ||
		w.j.completed != 2 {
		t.Errorf("compacted %s %q in %d writes, %d completions", w.o.rows[key], w.o.data[key],
			w.o.puts, w.j.completed)
	}
	if err := w.svc.Compact(t.Context(), w.compactJob(t, kernel.RunStateInitializing,
		4)); err != nil || w.o.puts != 1 || w.j.completed != 3 {
		t.Errorf("a state without chunks: %v, %d writes", err, w.o.puts)
	}
	if err := w.svc.Compact(t.Context(), queue.Job{Payload: []byte("{")}); err == nil {
		t.Error("a payload that does not decode compacted")
	}
}

// TestUnitLogsCompactRecovers checks an earlier attempt's object: one
// written whole is committed, one never finished is deleted for a later
// attempt; a chunk retention removed, and chunks a purge took, end the
// job with nothing committed.
func TestUnitLogsCompactRecovers(t *testing.T) {
	for name, c := range map[string]struct {
		before   string
		expired  bool
		locked   []int64
		snooze   bool
		want     string
		complete int
	}{
		"written whole":  {before: "uploaded", want: "live", complete: 1},
		"never finished": {before: "pending", snooze: true, want: "deleting"},
		"chunk expired":  {expired: true, want: "pending", complete: 1},
		"chunks purged":  {locked: []int64{0}, want: "uploaded", complete: 1},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld()
			w.chunk(5, 0, "a\n", 0)
			if c.expired {
				w.chunk(5, 1, "expired", 2)
			} else {
				w.chunk(5, 1, "b\n", 0)
			}
			w.s.locked = c.locked
			key := report.LogKey(w.s.run.Account, w.s.run.ID, kernel.RunStatePlanning, 5)
			if c.before != "" {
				w.o.rows[key], w.o.data[key] = c.before, []byte("a\nb\n")
			}
			err := w.svc.Compact(t.Context(), w.compactJob(t, kernel.RunStatePlanning, 5))
			if c.snooze != (err != nil) || err != nil && !strings.Contains(err.Error(), "snoozed") {
				t.Errorf("Compact: %v", err)
			}
			if w.o.rows[key] != c.want || w.j.completed != c.complete {
				t.Errorf("the object is %q, the job completed %d times", w.o.rows[key],
					w.j.completed)
			}
		})
	}
}

// TestUnitLogsCompactFailures checks that each failure leaves the job to
// be tried again, and that another attempt's commit found under the
// chunks' locks completes the job.
func TestUnitLogsCompactFailures(t *testing.T) {
	for _, c := range []struct {
		store, objects string
		live           int
		jobs           error
		before         string
	}{
		{store: "Chunks"},
		{live: 1},
		{objects: "Put"},
		{store: "LockChunks"},
		{live: 2},
		{objects: "Commit"},
		{jobs: errDown},
		{objects: "Delete", before: "pending"},
	} {
		w := newWorld()
		w.chunk(5, 0, "a\n", 0)
		key := report.LogKey(w.s.run.Account, w.s.run.ID, kernel.RunStatePlanning, 5)
		if c.before != "" {
			w.o.rows[key] = c.before
		}
		w.s.fail, w.o.fail, w.o.failLive, w.j.err = c.store, c.objects, c.live, c.jobs
		if err := w.svc.Compact(t.Context(), w.compactJob(t, kernel.RunStatePlanning,
			5)); !errors.Is(err, errDown) {
			t.Errorf("Compact with %+v failing: %v", c, err)
		}
	}
	w := newWorld()
	w.chunk(5, 0, "a\n", 0)
	key := report.LogKey(w.s.run.Account, w.s.run.ID, kernel.RunStatePlanning, 5)
	w.o.rows[key] = "uploaded"
	// Another attempt commits between this one's write and its lock.
	w.o.onLive = func(n int) {
		if n == 2 {
			w.o.rows[key] = "live"
		}
	}
	if err := w.svc.Compact(t.Context(), w.compactJob(t, kernel.RunStatePlanning,
		5)); err != nil || w.j.completed != 1 || w.o.puts != 0 || w.o.rows[key] != "live" {
		t.Errorf("an object committed meanwhile: %v, %d completions", err, w.j.completed)
	}
}
