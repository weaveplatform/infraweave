package runlog

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/platform/mask"
)

// MaxChunk is the largest chunk a Log uploads, the worker API's limit of
// a log-chunk upload.
const MaxChunk = 1 << 20

// Cut is how long a chunk collects lines after its first before it is
// uploaded, so a reader of a quiet run sees its last line this soon.
const Cut = 2 * time.Second

// Upload retries: a chunk the worker API or the store could not take for
// now is sent again for up to retryFor, waiting from retryWait to
// retryWaitMax between sends, as the runner's reports are.
const (
	retryFor     = 10 * time.Minute
	retryWait    = time.Second
	retryWaitMax = 30 * time.Second
)

// queued bounds the chunks waiting for their upload. A full queue holds
// the reader, and through the pipe the run's output, rather than growing.
const queued = 8

// addMask starts the line that names words to mask from then on.
var addMask = []byte("::add-mask")

// errAnswer marks an upload declaration answered with other than one
// URL.
var errAnswer = errors.New("the worker API answered no upload URL")

// API is the worker API's side of a run's log; *client.Client is one.
type API interface {
	UploadURLs(
		ctx context.Context,
		token, run string,
		in wire.UploadURLsRequest,
	) ([]wire.SignedUpload, error)
	Put(ctx context.Context, u wire.SignedUpload, body []byte) error
	LogChunks(ctx context.Context, token, run string, in wire.LogChunksRequest) error
}

// Deps are what a Log needs: the worker API and the run's ID and token
// it uploads with, where masked lines go, the runner's log, and the
// Masker the run's secrets made.
type Deps struct {
	API    API
	Out    io.Writer
	Log    *slog.Logger
	Run    string
	Token  string
	Masker *mask.Masker
}

// Log is a run's log. What is written to its File, a pipe, is read a
// line at a time, masked, written to Out, and, while a state is open,
// collected in chunks that are uploaded and committed in order.
type Log struct {
	d     Deps
	r     io.ReadCloser
	w     io.WriteCloser
	file  *os.File
	nonce []byte
	// synced carries the reader's word that it read a barrier; read and
	// sent close when the reader and the uploader end.
	synced chan struct{}
	read   chan struct{}
	sent   chan struct{}
	queue  chan item

	// barrier serializes barriers; final is whether the one in the pipe
	// ends the reader.
	barrier sync.Mutex
	final   atomic.Bool

	mu      sync.Mutex
	lines   *mask.Lines
	closed  bool
	open    bool
	version int64
	seq     int64
	buf     []byte
	first   time.Time
	last    time.Time
	// gen names the chunk being collected, so the timer of one already
	// cut cuts nothing.
	gen   int
	timer *time.Timer
}

// item is a chunk to upload, or, with done, the point every chunk queued
// before it has been sent by.
type item struct {
	c    chunk
	done chan struct{}
}

type chunk struct {
	version, seq int64
	body         []byte
	first, last  time.Time
}

// New starts a run's log, which uploads with ctx until it is closed.
func New(ctx context.Context, d Deps) (*Log, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("start the run's log: %w", err)
	}
	l := start(ctx, d, r, w)
	l.file = w
	return l, nil
}

// start starts the log of what is written to w, read from r.
func start(ctx context.Context, d Deps, r io.ReadCloser, w io.WriteCloser) *Log {
	m := d.Masker
	if m == nil {
		m = mask.New()
	}
	l := &Log{
		d: d, r: r, w: w, lines: m.Lines(),
		// A barrier is a line no output holds: the runner's own nonce.
		nonce:  []byte("infraweave-log-barrier-" + rand.Text()),
		synced: make(chan struct{}, 1), read: make(chan struct{}), sent: make(chan struct{}),
		queue: make(chan item, queued),
	}
	go l.reading()
	go l.uploading(ctx)
	return l
}

// File is the pipe's write end, which the run's commands write their
// output to: as a file, a process writes it with no copy between.
func (l *Log) File() *os.File { return l.file }

// Write writes p to the log, after what was written before it.
func (l *Log) Write(p []byte) (int, error) {
	n, err := l.w.Write(p)
	if err != nil {
		return n, fmt.Errorf("write the run's log: %w", err)
	}
	return n, nil
}

// Add masks words too, from the next line read on.
func (l *Log) Add(words ...[]byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines.Add(words...)
}

// Begin opens state version's chunks, from sequence number 0, once every
// line written before it is read: lines read while no state is open go to
// Out alone, and without the wait a line written just before Begin would
// land in the state's first chunk. The state opens even when the wait
// ends with ctx, whose error Begin answers.
func (l *Log) Begin(ctx context.Context, version int64) error {
	err := l.sync(ctx, false)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.open, l.version, l.seq = true, version, 0
	return err
}

// End closes the open state: once every line written before it is read,
// it cuts the last chunk and answers when every chunk queued has been
// uploaded and committed, or dropped after its retries.
//
// ref: decision 0008 § "Logs and artifacts"
func (l *Log) End(ctx context.Context) error {
	return l.end(ctx, false)
}

// Close ends the log as End does, and stops reading and uploading; it
// answers once the reader has ended, so a later End or Begin finds the
// log closed rather than writing a barrier to the closed pipe. Closing it
// again does nothing.
func (l *Log) Close(ctx context.Context) error {
	err := l.end(ctx, true)
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return err
	}
	l.closed = true
	if l.timer != nil {
		l.timer.Stop()
	}
	close(l.queue)
	l.mu.Unlock()
	select {
	case <-l.sent:
	case <-ctx.Done():
	}
	_ = l.w.Close()
	_ = l.r.Close()
	<-l.read
	return err
}

func (l *Log) end(ctx context.Context, final bool) error {
	if err := l.sync(ctx, final); err != nil {
		return err
	}
	done := make(chan struct{})
	l.mu.Lock()
	if !l.closed {
		l.cut()
		l.queue <- item{done: done}
	} else {
		close(done)
	}
	l.open = false
	l.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("flush the run's log: %w", ctx.Err())
	}
}

// sync writes a barrier to the pipe and waits until the reader reads it,
// so every line written before it has been read. A final barrier ends the
// reader.
func (l *Log) sync(ctx context.Context, final bool) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("flush the run's log: %w", err)
	}
	l.barrier.Lock()
	defer l.barrier.Unlock()
	// An ended reader is checked alone first: a select with a word left in
	// synced as well would pick either, and write to a closed pipe.
	select {
	case <-l.read:
		return nil
	default:
	}
	select {
	case <-l.synced:
	default:
	}
	l.final.Store(final)
	if _, err := l.w.Write(append(bytes.Clone(l.nonce), '\n')); err != nil {
		return fmt.Errorf("flush the run's log: %w", err)
	}
	select {
	case <-l.synced:
	case <-l.read:
	case <-ctx.Done():
		return fmt.Errorf("flush the run's log: %w", ctx.Err())
	}
	return nil
}

// reading reads the pipe a line at a time, keeping at most mask.MaxLine
// bytes of each, until a final barrier or the pipe's end.
func (l *Log) reading() {
	defer close(l.read)
	buf := make([]byte, 32<<10)
	var line, tail []byte
	// raw counts the line's bytes as written, past the cap too, and tail
	// keeps its last ones, so a barrier after a long line is still seen.
	raw := 0
	for {
		n, err := l.r.Read(buf)
		for p := buf[:n]; len(p) > 0; {
			i := bytes.IndexByte(p, '\n')
			part := p
			if i >= 0 {
				part = p[:i]
			}
			line = append(line, part[:min(len(part), mask.MaxLine-len(line))]...)
			raw += len(part)
			tail = append(tail, part...)
			if len(tail) > 2*len(l.nonce) {
				tail = append(tail[:0], tail[len(tail)-len(l.nonce):]...)
			}
			if i < 0 {
				break
			}
			p = p[i+1:]
			if !bytes.HasSuffix(tail, l.nonce) {
				l.line(line)
			} else if l.barrierRead(line[:min(len(line), raw-len(l.nonce))]) {
				return
			}
			line, tail, raw = line[:0], tail[:0], 0
		}
		if err != nil {
			if len(line) > 0 {
				l.line(line)
			}
			return
		}
	}
}

// barrierRead takes the output a barrier ended, which had no newline of
// its own, and tells the barrier's writer; it answers whether the barrier
// was the final one.
func (l *Log) barrierRead(before []byte) bool {
	if len(before) > 0 {
		l.line(before)
	}
	final := l.final.Load()
	select {
	case l.synced <- struct{}{}:
	default:
	}
	return final
}

// line masks one line, after taking the words it adds when it is an
// ::add-mask command, so the command shows them masked; writes it to Out;
// and adds it to the open state's chunk.
//
// ref: decision 0013 § "Masking"
func (l *Log) line(raw []byte) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if rest, ok := bytes.CutPrefix(bytes.TrimLeft(raw, " \t"), addMask); ok &&
		(len(rest) == 0 || rest[0] == ' ' || rest[0] == '\t') {
		l.lines.Add(bytes.Fields(rest)...)
	}
	out := append(l.lines.Mask(raw), '\n')
	_, _ = l.d.Out.Write(out)
	if !l.open || l.closed {
		return
	}
	if len(l.buf)+len(out) > MaxChunk {
		l.cut()
	}
	if len(l.buf) == 0 {
		l.first = now
		l.gen++
		gen := l.gen
		l.timer = time.AfterFunc(Cut, func() { l.tick(gen) })
	}
	l.buf = append(l.buf, out...)
	l.last = now
}

// tick cuts the chunk gen names when its time is up.
func (l *Log) tick(gen int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if gen == l.gen && !l.closed {
		l.cut()
	}
}

// cut queues the chunk being collected, when it holds a line, and starts
// the next. l.mu is held.
func (l *Log) cut() {
	if len(l.buf) == 0 {
		return
	}
	l.timer.Stop()
	l.gen++
	l.queue <- item{c: chunk{
		version: l.version, seq: l.seq, body: l.buf, first: l.first, last: l.last,
	}}
	l.seq++
	l.buf = nil
}

// uploading uploads the queued chunks in order until the queue closes;
// once ctx ends it drops them.
func (l *Log) uploading(ctx context.Context) {
	defer close(l.sent)
	for it := range l.queue {
		switch {
		case it.done != nil:
			close(it.done)
		case ctx.Err() == nil:
			l.upload(ctx, it.c)
		}
	}
}

// upload declares, PUTs and commits c, retrying each step that could not
// be taken for now: a declaration again, which signs the same object, a
// failed PUT from a new declaration, and a commit alone, which skips a
// chunk already indexed. A chunk that cannot be taken is dropped with a
// warning; readers skip its sequence number once the state has ended.
func (l *Log) upload(ctx context.Context, c chunk) {
	sum := sha256.Sum256(c.body)
	name := strconv.FormatInt(c.seq, 10)
	decl := wire.UploadURLsRequest{StateVersion: c.version, Uploads: []wire.Upload{{
		Kind: wire.UploadKindLogChunk, Name: name, Size: int64(len(c.body)),
		Sha256: hex.EncodeToString(sum[:]),
	}}}
	commit := wire.LogChunksRequest{StateVersion: c.version, Chunks: []wire.LogChunk{{
		Seq: c.seq, FirstAt: c.first.Unix(), LastAt: c.last.Unix(),
	}}}
	deadline := time.Now().Add(retryFor)
	wait := retryWait
	uploaded := false
	for {
		err := l.send(ctx, decl, commit, c.body, &uploaded)
		if err == nil {
			return
		}
		if !transient(ctx, err) || time.Now().Add(wait).After(deadline) {
			l.d.Log.WarnContext(ctx, "log chunk dropped", "run", l.d.Run,
				"stateVersion", c.version, "seq", c.seq, "error", err)
			return
		}
		l.d.Log.WarnContext(ctx, "log chunk not taken", "run", l.d.Run,
			"stateVersion", c.version, "seq", c.seq, "error", err, "retry", wait)
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		wait = min(wait*2, retryWaitMax)
	}
}

// send takes one chunk through the worker API: its declaration and PUT,
// unless uploaded says they were taken, and its commit.
func (l *Log) send(
	ctx context.Context,
	decl wire.UploadURLsRequest,
	commit wire.LogChunksRequest,
	body []byte,
	uploaded *bool,
) error {
	if !*uploaded {
		ups, err := l.d.API.UploadURLs(ctx, l.d.Token, l.d.Run, decl)
		if err != nil {
			return err //nolint:wrapcheck // client names the call
		}
		if len(ups) != 1 {
			return errAnswer
		}
		if err := l.d.API.Put(ctx, ups[0], body); err != nil {
			return err //nolint:wrapcheck // client names the upload
		}
		*uploaded = true
	}
	//nolint:wrapcheck // client names the call
	return l.d.API.LogChunks(ctx, l.d.Token, l.d.Run, commit)
}

// transient reports whether err is a failure a later send may not meet:
// no answer, or the server's or the store's being unavailable for now.
func transient(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	s := client.Status(err)
	return s == 0 && !errors.Is(err, errAnswer) || s == http.StatusTooManyRequests ||
		s >= http.StatusInternalServerError
}
