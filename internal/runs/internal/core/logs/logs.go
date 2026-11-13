package logs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore/blobs"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/lifecycle"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/report"
)

// Store reads runs and their log index. Each method joins the transaction
// its context carries.
type Store interface {
	// Get reads acct's run id; read.ErrNotFound when acct lacks it.
	Get(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (lifecycle.Run, error)
	// Entered reads the latest state version at which acct's run entered
	// state, and false when it never did.
	Entered(ctx context.Context, acct kernel.AccountID, run kernel.ULID,
		state kernel.RunState) (int64, bool, error)
	// StateAt reads the state acct's run entered at state version
	// version; lifecycle.ErrNotFound when it has no such transition.
	StateAt(ctx context.Context, acct kernel.AccountID, run kernel.ULID,
		version int64) (kernel.RunState, error)
	// Chunks reads up to limit of acct's run's log chunks of state version
	// version, from sequence number from on, in sequence order.
	Chunks(ctx context.Context, acct kernel.AccountID, run kernel.ULID, version, from int64,
		limit int) ([]report.Chunk, error)
	// LockChunks reads the sequence numbers of acct's run's log chunks of
	// state version version, in order, and locks their rows until the
	// transaction ends.
	LockChunks(ctx context.Context, acct kernel.AccountID, run kernel.ULID,
		version int64) ([]int64, error)
}

// Objects reads, signs and writes objects of the run-logs store, as
// blob_objects records them; blobs.Objects is one.
type Objects interface {
	Open(ctx context.Context, store objectstore.Name, key string) (io.ReadCloser, error)
	SignGet(ctx context.Context, store objectstore.Name, key string) (objectstore.Signed, error)
	Live(ctx context.Context, store objectstore.Name, key string) (bool, error)
	Put(ctx context.Context, store objectstore.Name, key string, r io.Reader, size int64) (
		objectstore.Object, error)
	Commit(ctx context.Context, store objectstore.Name, key string) error
	Delete(ctx context.Context, store objectstore.Name, key string) error
}

// Jobs completes a compaction in the transaction ctx carries; a
// queue.Queue is one.
type Jobs interface {
	CompleteTx(ctx context.Context, j queue.Job) error
}

// Deps are what the logs read and write through: the database a
// compaction commits on, the runs' storage, the objects and the queue.
type Deps struct {
	DB      tx.Beginner
	Store   Store
	Objects Objects
	Jobs    Jobs
}

// Service reads a run's logs by state and compacts each state's log once
// the run has left it.
//
// ref: decision 0008 § "Logs and artifacts"
type Service struct{ d Deps }

// New returns the service over d.
func New(d Deps) *Service { return &Service{d: d} }

// A page holds whole chunks, at least one when one can be read, up to
// pageBytes of them or pageChunks of them. A chunk is at most 1 MiB, the
// upload limit of a log chunk.
const (
	pageBytes  = 1 << 20
	pageChunks = 64
)

// retryAfter is how long a compaction waits for an earlier attempt's
// object to be removed before writing its own.
const retryAfter = time.Minute

// Query is a page of a run's log: the state, the state version, nil for
// the latest at which the run entered the state, the chunk to read from,
// and whether to strip each line's ANSI escape sequences.
type Query struct {
	State    kernel.RunState
	Version  *int64
	From     int64
	Sanitize bool
}

// Line is a logged line and when the worker began the chunk that holds
// it, the nearest time the index keeps.
type Line struct {
	Message string
	At      time.Time
}

// Page is a page of a state's log. Exists reports whether the run entered
// the state at Version; Ongoing whether it is still there; Expired
// whether retention removed lines the page would hold; HasMore whether
// more lines can be read now. Next is the chunk the next page reads from.
type Page struct {
	Lines   []Line
	Version int64
	Next    int64
	Exists  bool
	Ongoing bool
	Expired bool
	HasMore bool
}

// Finished reports whether the page holds the state's last lines.
func (p Page) Finished() bool { return !p.Ongoing && !p.HasMore }

// More reports whether a next page can hold lines, now or later.
func (p Page) More() bool { return p.Ongoing || p.HasMore }

// Download is where a state's whole log downloads from: URL once it is
// compacted; otherwise Ongoing while the run is in the state or the
// compaction is pending, and Expired when the state wrote no chunk or
// retention removed them.
type Download struct {
	URL     string
	Ongoing bool
	Expired bool
}

// Page reads a page of p's run raw's log of q.State, the chunks of its
// state version from q.From on in sequence order. While the run is in the
// state, the page ends before a sequence number not yet indexed, so a
// chunk indexed out of order is read in its place. A state the run never
// entered at that version answers an empty page with Exists false. The
// route has decided SPACE_READ on the run's stack's space.
func (s *Service) Page(ctx context.Context, p kernel.Principal, raw string, q Query) (Page,
	error,
) {
	run, err := s.get(ctx, p.AccountID, raw)
	if err != nil {
		return Page{}, err
	}
	out := Page{Lines: []Line{}, Next: q.From}
	out.Version, out.Exists, err = s.version(ctx, run, q.State, q.Version)
	if err != nil || !out.Exists {
		return out, err
	}
	out.Ongoing = run.State == q.State && run.Version == out.Version
	chunks, err := s.d.Store.Chunks(ctx, run.Account, run.ID, out.Version, q.From, pageChunks+1)
	if err != nil {
		return Page{}, err //nolint:wrapcheck // the store names the read
	}
	var size int64
	i := 0
	for ; i < len(chunks) && i < pageChunks; i++ {
		c := chunks[i]
		if out.Ongoing && c.Seq != out.Next || size > 0 && size+c.Bytes > pageBytes {
			break
		}
		lines, err := s.lines(ctx, c, q.Sanitize)
		switch {
		case errors.Is(err, objectstore.ErrNotFound):
			out.Expired = true
		case err != nil:
			return Page{}, err
		}
		out.Lines = append(out.Lines, lines...)
		size += c.Bytes
		out.Next = c.Seq + 1
	}
	out.HasMore = i < len(chunks) && (!out.Ongoing || chunks[i].Seq == out.Next)
	return out, nil
}

// lines reads chunk c's lines, each stamped with when its first was
// written, stripping their ANSI escape sequences when sanitize is set.
func (s *Service) lines(ctx context.Context, c report.Chunk, sanitize bool) ([]Line, error) {
	r, err := s.d.Objects.Open(ctx, objectstore.RunLogs, c.Key)
	if err != nil {
		return nil, err //nolint:wrapcheck // blobs names the object
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("runs: read log chunk %s: %w", c.Key, err)
	}
	text := strings.TrimSuffix(string(b), "\n")
	if text == "" {
		return nil, nil
	}
	split := strings.Split(text, "\n")
	out := make([]Line, len(split))
	for i, l := range split {
		if sanitize {
			l = Sanitize(l)
		}
		out[i] = Line{Message: l, At: c.FirstAt}
	}
	return out, nil
}

// Download answers where p's run raw's whole log of state at version,
// nil for the latest at which the run entered it, downloads from. The
// route has decided SPACE_READ on the run's stack's space.
func (s *Service) Download(
	ctx context.Context,
	p kernel.Principal,
	raw string,
	state kernel.RunState,
	version *int64,
) (Download, error) {
	run, err := s.get(ctx, p.AccountID, raw)
	if err != nil {
		return Download{}, err
	}
	v, exists, err := s.version(ctx, run, state, version)
	if err != nil || !exists {
		return Download{Expired: true}, err
	}
	signed, err := s.d.Objects.SignGet(ctx, objectstore.RunLogs,
		report.LogKey(run.Account, run.ID, state, v))
	switch {
	case err == nil:
		return Download{URL: signed.URL}, nil
	case !errors.Is(err, objectstore.ErrNotFound):
		return Download{}, err //nolint:wrapcheck // blobs names the object
	case run.State == state && run.Version == v:
		return Download{Ongoing: true}, nil
	}
	first, err := s.d.Store.Chunks(ctx, run.Account, run.ID, v, 0, 1)
	if err != nil || len(first) == 0 {
		return Download{Expired: true}, err //nolint:wrapcheck // the store names the read
	}
	// Retention removes a state's chunks oldest first, so a first chunk
	// still live means the compaction is pending, not that lines expired.
	live, err := s.d.Objects.Live(ctx, objectstore.RunLogs, first[0].Key)
	if err != nil {
		return Download{}, err //nolint:wrapcheck // blobs names the object
	}
	return Download{Ongoing: live, Expired: !live}, nil
}

// get reads acct's run raw; read.ErrNotFound when raw is no run ID.
func (s *Service) get(ctx context.Context, acct kernel.AccountID, raw string) (lifecycle.Run,
	error,
) {
	id, err := kernel.ParseULID(raw)
	if err != nil {
		return lifecycle.Run{}, fmt.Errorf("%w: %q", read.ErrNotFound, raw)
	}
	return s.d.Store.Get(ctx, acct, id) //nolint:wrapcheck // the store names the read
}

// version is the state version run entered state at: version when the
// run entered state there, else the latest at which it entered state
// when version is nil; false when it did not.
func (s *Service) version(
	ctx context.Context,
	run lifecycle.Run,
	state kernel.RunState,
	version *int64,
) (int64, bool, error) {
	if version == nil {
		//nolint:wrapcheck // the store names the read
		return s.d.Store.Entered(ctx, run.Account, run.ID, state)
	}
	at, err := s.d.Store.StateAt(ctx, run.Account, run.ID, *version)
	switch {
	case errors.Is(err, lifecycle.ErrNotFound):
		return *version, false, nil
	case err != nil:
		return 0, false, err //nolint:wrapcheck // the store names the read
	}
	return *version, at == state, nil
}

// Compact is lifecycle.CompactKind's handler. It writes the indexed
// chunks of the job's state, in sequence order, to the state's LogKey,
// then commits the object and completes the job in one transaction that
// locks the chunks' rows, so an account's purge, which deletes the
// object with them, finds it live. A compacted object already live, a
// state with no chunk, and a chunk retention removed end the job with
// nothing written. A key holding an earlier attempt's object is
// committed when that write finished, and otherwise deleted so a later
// attempt writes it again. A payload that does not decode never will, so
// its job dies at once.
func (s *Service) Compact(ctx context.Context, j queue.Job) error {
	var c lifecycle.Compact
	if err := json.Unmarshal(j.Payload, &c); err != nil {
		//nolint:wrapcheck // marks it
		return queue.Permanent(fmt.Errorf("runs: %s %d: %w", j.Kind, j.ID, err))
	}
	acct := kernel.AccountID{ULID: j.Account}
	key := report.LogKey(acct, c.Run, c.State, c.StateVersion)
	chunks, err := s.d.Store.Chunks(ctx, acct, c.Run, c.StateVersion, 0, math.MaxInt32)
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	live := false
	if len(chunks) > 0 {
		if live, err = s.d.Objects.Live(ctx, objectstore.RunLogs, key); err != nil {
			return err //nolint:wrapcheck // blobs names the object
		}
	}
	if len(chunks) == 0 || live {
		return s.complete(ctx, j)
	}
	in := &stream{ctx: ctx, objects: s.d.Objects, chunks: chunks}
	var size int64
	for _, ch := range chunks {
		size += ch.Bytes
	}
	_, err = s.d.Objects.Put(ctx, objectstore.RunLogs, key, in, size)
	in.close()
	switch {
	case errors.Is(in.err, objectstore.ErrNotFound):
		return s.complete(ctx, j)
	case err != nil && !errors.Is(err, blobs.ErrKeyInUse):
		return err //nolint:wrapcheck // blobs names the object
	}
	seqs := make([]int64, len(chunks))
	for i, ch := range chunks {
		seqs[i] = ch.Seq
	}
	retry := false
	err = tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		retry = false
		locked, err := s.d.Store.LockChunks(ctx, acct, c.Run, c.StateVersion)
		if err != nil {
			return err //nolint:wrapcheck // the store names the read
		}
		if !slices.Equal(locked, seqs) {
			// The account's purge or the run's deletion took chunks; the
			// purge deletes the object too, and retention bounds it.
			return s.d.Jobs.CompleteTx(ctx, j) //nolint:wrapcheck // the queue names the job
		}
		// The chunks' locks order this check after another attempt's
		// commit, so a live object is never taken for a stale one.
		if live, err := s.d.Objects.Live(ctx, objectstore.RunLogs, key); err != nil || live {
			if err != nil {
				return err //nolint:wrapcheck // blobs names the object
			}
			return s.d.Jobs.CompleteTx(ctx, j) //nolint:wrapcheck // as above
		}
		err = s.d.Objects.Commit(ctx, objectstore.RunLogs, key)
		if errors.Is(err, blobs.ErrNotUploaded) {
			retry = true
			return s.d.Objects.Delete(ctx, objectstore.RunLogs, key) //nolint:wrapcheck // as above
		}
		if err != nil {
			return err //nolint:wrapcheck // blobs names the object
		}
		return s.d.Jobs.CompleteTx(ctx, j) //nolint:wrapcheck // the queue names the job
	})
	if err == nil && retry {
		return queue.Snooze(retryAfter) //nolint:wrapcheck // marks it
	}
	return err //nolint:wrapcheck // each step names itself
}

// complete completes j, which wrote nothing.
func (s *Service) complete(ctx context.Context, j queue.Job) error {
	//nolint:wrapcheck // the queue names the job
	return tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		return s.d.Jobs.CompleteTx(ctx, j)
	})
}

// stream reads chunks' objects one after another, opening each when the
// one before ends; err is the first error an open or a read met, which
// the writer reading it may report otherwise.
type stream struct {
	ctx     context.Context //nolint:containedctx // an io.Reader's reads take none
	objects Objects
	chunks  []report.Chunk
	cur     io.ReadCloser
	err     error
}

func (s *stream) Read(p []byte) (int, error) {
	for {
		if s.cur == nil {
			if len(s.chunks) == 0 {
				return 0, io.EOF
			}
			r, err := s.objects.Open(s.ctx, objectstore.RunLogs, s.chunks[0].Key)
			if err != nil {
				s.err = err
				return 0, err //nolint:wrapcheck // blobs names the object
			}
			s.cur, s.chunks = r, s.chunks[1:]
		}
		n, err := s.cur.Read(p)
		if errors.Is(err, io.EOF) {
			_ = s.cur.Close()
			s.cur = nil
			if n == 0 {
				continue
			}
			err = nil
		}
		if err != nil {
			s.err = err
		}
		return n, err //nolint:wrapcheck // the driver names the object
	}
}

// close closes the chunk being read, if any.
func (s *stream) close() {
	if s.cur != nil {
		_ = s.cur.Close()
	}
}
