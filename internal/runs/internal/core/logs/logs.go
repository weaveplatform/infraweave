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
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// ErrRunning reports a log deletion of a run that has not ended.
var ErrRunning = errors.New("runs: the run has not ended")

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
	// Extent reads what the index holds of acct's run's log of state
	// version version, its line before chunk from being the last line of
	// the chunk indexed before it; lifecycle.ErrNotFound when the run has
	// no transition to version.
	Extent(ctx context.Context, acct kernel.AccountID, run kernel.ULID, version,
		from int64) (Extent, error)
	// LockChunks reads the sequence numbers of acct's run's log chunks of
	// state version version, in order, and locks their rows until the
	// transaction ends.
	LockChunks(ctx context.Context, acct kernel.AccountID, run kernel.ULID,
		version int64) ([]int64, error)
	// Lock reads acct's run id and locks its row FOR UPDATE;
	// lifecycle.ErrNotFound when acct lacks it.
	Lock(ctx context.Context, acct kernel.AccountID, id kernel.ULID) (lifecycle.Run, error)
	// DeleteLogs deletes run's log chunks and observability artifacts,
	// records at as when its logs were deleted, and answers the objects
	// they recorded, with each state's compacted log, which the caller
	// deletes in the same transaction.
	DeleteLogs(ctx context.Context, run lifecycle.Run, at time.Time) ([]report.Object, error)
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
// compaction and a deletion commit on, the runs' storage, the objects,
// the queue and the stacks a deletion finds its run's stack in;
// Retention, objectstore.retention.run-logs, how long the run-logs store
// keeps an object, 0 until its record goes; and Now, the clock, time.Now
// when nil.
type Deps struct {
	DB        tx.Beginner
	Store     Store
	Objects   Objects
	Jobs      Jobs
	Stacks    stacksapi.Stacks
	Retention time.Duration
	Now       func() time.Time
}

// Service reads a run's logs by state and compacts each state's log once
// the run has left it.
//
// ref: decision 0008 § "Logs and artifacts"
type Service struct{ d Deps }

// New returns the service over d.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Service{d: d}
}

// RetentionDays is how many days the run-logs store keeps a log, a part
// of a day counting as a whole one, so a retention under a day answers 1
// rather than 0, which answers a retention of none: logs kept until their
// run goes.
//
// ref: decision 0008 § "Logs and artifacts"
func (s *Service) RetentionDays() int32 {
	const day = 24 * time.Hour
	days := s.d.Retention / day
	if s.d.Retention%day != 0 {
		days++
	}
	return int32(days) //nolint:gosec // a Duration spans at most 106,752 days
}

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

// Extent is what the index holds of a state version's log. Chunks is how
// many chunks the worker cut it into: the count its report of the state's
// end named, or one more than the highest sequence number indexed when
// that is more or no report came. Stored is the key of the first chunk
// stored, empty when none is; Before when the line before a chunk was
// written, or when the run entered the state if no chunk is indexed
// before it; Entered when the run entered the state.
type Extent struct {
	Chunks  int64
	Stored  string
	Before  time.Time
	Entered time.Time
}

// Missing is the line a reader finds in place of each chunk of a state the
// run has left that holds no lines: one the worker committed as lost, or
// one below Extent.Chunks it never committed. The server writes it, so it
// carries no value of the run's.
const Missing = "infraweave: part of this log could not be stored."

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
// whether retention removed lines the page would hold, or a caller
// deleted them, which Deleted reports; HasMore whether more lines can be
// read now. Next is the chunk the next page reads from.
type Page struct {
	Lines   []Line
	Version int64
	Next    int64
	Exists  bool
	Ongoing bool
	Expired bool
	Deleted bool
	HasMore bool
}

// Finished reports whether the page holds the state's last lines.
func (p Page) Finished() bool { return !p.Ongoing && !p.HasMore }

// More reports whether a next page can hold lines, now or later.
func (p Page) More() bool { return p.Ongoing || p.HasMore }

// Download is where a state's whole log downloads from: URL once it is
// compacted; otherwise Ongoing while the run is in the state or the
// compaction is pending, and Expired when the state wrote no chunk,
// retention removed them or a caller deleted them, which Deleted
// reports.
type Download struct {
	URL     string
	Ongoing bool
	Expired bool
	Deleted bool
}

// Page reads a page of p's run raw's log of q.State, the chunks of its
// state version from q.From on in sequence order, a lost chunk read as
// Missing. While the run is in the state, the page ends before a sequence
// number not yet indexed, so a chunk indexed out of order is read in its
// place; once the run has left it, each sequence number below
// Extent.Chunks not indexed is read as Missing, stamped with when the line
// before it was written. A state the run never entered at that version
// answers an empty page with Exists false. The route has decided
// SPACE_READ on the run's stack's space.
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
	if run.LogsDeletedAt != nil {
		out.Expired, out.Deleted = true, true
		return out, nil
	}
	out.Ongoing = run.State == q.State && run.Version == out.Version
	chunks, err := s.d.Store.Chunks(ctx, run.Account, run.ID, out.Version, q.From, pageChunks+1)
	if err != nil {
		return Page{}, err //nolint:wrapcheck // the store names the read
	}
	// While the run is in the state, a sequence number not indexed is
	// still to come, so the page ends there rather than at an extent.
	ext := Extent{Chunks: math.MaxInt64}
	if !out.Ongoing {
		if ext, err = s.d.Store.Extent(ctx, run.Account, run.ID, out.Version, q.From); err != nil {
			return Page{}, err //nolint:wrapcheck // the store names the read
		}
	}
	var size int64
	before, i := ext.Before, 0
	for range pageChunks {
		indexed := i < len(chunks) && chunks[i].Seq == out.Next
		if !indexed && (out.Ongoing || out.Next >= ext.Chunks) ||
			indexed && size > 0 && size+chunks[i].Bytes > pageBytes {
			break
		}
		if !indexed {
			out.Lines = append(out.Lines, Line{Message: Missing, At: before})
			out.Next++
			continue
		}
		c := chunks[i]
		lines, err := s.lines(ctx, c, q.Sanitize)
		switch {
		case errors.Is(err, objectstore.ErrNotFound):
			out.Expired = true
		case err != nil:
			return Page{}, err
		}
		out.Lines = append(out.Lines, lines...)
		size += c.Bytes
		before = c.LastAt
		out.Next, i = c.Seq+1, i+1
	}
	if out.Ongoing {
		out.HasMore = i < len(chunks) && chunks[i].Seq == out.Next
	} else {
		out.HasMore = out.Next < ext.Chunks
	}
	return out, nil
}

// lines reads chunk c's lines, each stamped with when its first was
// written, stripping their ANSI escape sequences when sanitize is set; a
// lost chunk's line is Missing.
func (s *Service) lines(ctx context.Context, c report.Chunk, sanitize bool) ([]Line, error) {
	if c.Lost {
		return []Line{{Message: Missing, At: c.FirstAt}}, nil
	}
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
	if run.LogsDeletedAt != nil {
		return Download{Expired: true, Deleted: true}, nil
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
	ext, err := s.d.Store.Extent(ctx, run.Account, run.ID, v, 0)
	if err != nil || ext.Chunks == 0 {
		return Download{Expired: true}, err //nolint:wrapcheck // the store names the read
	}
	if ext.Stored == "" {
		// With no chunk stored, only the compacted log can expire, and it
		// is written after the run entered the state, so within the
		// retention of that the compaction is pending.
		pending := s.d.Retention == 0 || s.d.Now().Before(ext.Entered.Add(s.d.Retention))
		return Download{Ongoing: pending, Expired: !pending}, nil
	}
	// Retention removes a state's chunks oldest first, so a first chunk
	// still live means the compaction is pending, not that lines expired.
	live, err := s.d.Objects.Live(ctx, objectstore.RunLogs, ext.Stored)
	if err != nil {
		return Download{}, err //nolint:wrapcheck // blobs names the object
	}
	return Download{Ongoing: live, Expired: !live}, nil
}

// Profile answers where p's run raw's CPU profile of the latest
// execution of state downloads from: URL once the runner recorded it;
// otherwise Ongoing while the run is in that execution of the state, and
// Expired when it wrote no profile, retention removed it or a caller
// deleted the run's logs, which Deleted reports. The route has decided
// SPACE_READ on the run's stack's space.
//
// ref: decision 0008 § "Logs and artifacts"
func (s *Service) Profile(
	ctx context.Context,
	p kernel.Principal,
	raw string,
	state kernel.RunState,
) (Download, error) {
	run, err := s.get(ctx, p.AccountID, raw)
	if err != nil {
		return Download{}, err
	}
	if run.LogsDeletedAt != nil {
		return Download{Expired: true, Deleted: true}, nil
	}
	v, exists, err := s.version(ctx, run, state, nil)
	if err != nil || !exists {
		return Download{Expired: true}, err
	}
	signed, err := s.d.Objects.SignGet(ctx, objectstore.RunObservability,
		kernel.RunObjectKey(run.Account, run.ID, state, v, kernel.CPUProfileName(state)))
	switch {
	case err == nil:
		return Download{URL: signed.URL}, nil
	case !errors.Is(err, objectstore.ErrNotFound):
		return Download{}, err //nolint:wrapcheck // blobs names the object
	case run.State == state && run.Version == v:
		return Download{Ongoing: true}, nil
	}
	return Download{Expired: true}, nil
}

// Delete deletes the logs of p's run raw of stack slug ahead of their
// retention, in one transaction: its log chunks, each state's compacted
// log and its observability artifacts, with their records, the objects
// going once it commits, and records when, after which Page and Download
// answer Deleted. A run whose logs were deleted is answered as it is, and
// one that has not ended is ErrRunning. The route has decided SPACE_ADMIN
// on the account's top space.
//
// ref: decision 0008 § "Logs and artifacts"
func (s *Service) Delete(
	ctx context.Context,
	p kernel.Principal,
	slug, raw string,
) (lifecycle.Run, error) {
	id, err := kernel.ParseULID(raw)
	if err != nil {
		return lifecycle.Run{}, fmt.Errorf("%w: %q", read.ErrNotFound, raw)
	}
	var run lifecycle.Run
	err = tx.Do(ctx, s.d.DB, tx.Options{}, func(ctx context.Context) error {
		st, err := s.d.Stacks.BySlug(ctx, p.AccountID, slug)
		if err != nil {
			return err //nolint:wrapcheck // the stacks domain names the read
		}
		run, err = s.d.Store.Lock(ctx, p.AccountID, id)
		switch {
		case errors.Is(err, lifecycle.ErrNotFound) || err == nil && run.Stack != st.ID:
			return fmt.Errorf("%w: %s on %s", read.ErrNotFound, raw, slug)
		case err != nil:
			return err //nolint:wrapcheck // the store names the read
		case run.LogsDeletedAt != nil:
			return nil
		case !run.State.Terminal():
			return fmt.Errorf("%w: %s is %s", ErrRunning, run.ID, run.State)
		}
		at := s.d.Now().UTC().Truncate(time.Microsecond)
		objs, err := s.d.Store.DeleteLogs(ctx, run, at)
		if err != nil {
			return err //nolint:wrapcheck // the store names the write
		}
		for _, o := range objs {
			if err := s.d.Objects.Delete(ctx, o.Store, o.Key); err != nil {
				return err //nolint:wrapcheck // blobs names the object
			}
		}
		run.LogsDeletedAt, run.UpdatedAt = &at, at
		return nil
	})
	return run, err //nolint:wrapcheck // each step names itself
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
// with Missing's line in place of each chunk below Extent.Chunks that
// holds no lines, then commits the object and completes the job in one
// transaction that locks the chunks' rows, so an account's purge, which
// deletes the object with them, finds it live. A compacted object
// already live, a state with no chunk, a run deleted, and a chunk
// retention removed end the job with nothing written. A key holding an earlier attempt's object is
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
	ext, err := s.d.Store.Extent(ctx, acct, c.Run, c.StateVersion, 0)
	switch {
	case errors.Is(err, lifecycle.ErrNotFound):
		return s.complete(ctx, j)
	case err != nil:
		return err //nolint:wrapcheck // the store names the read
	}
	chunks, err := s.d.Store.Chunks(ctx, acct, c.Run, c.StateVersion, 0, math.MaxInt32)
	if err != nil {
		return err //nolint:wrapcheck // the store names the read
	}
	live := false
	if ext.Chunks > 0 {
		if live, err = s.d.Objects.Live(ctx, objectstore.RunLogs, key); err != nil {
			return err //nolint:wrapcheck // blobs names the object
		}
	}
	if ext.Chunks == 0 || live {
		return s.complete(ctx, j)
	}
	in, size := newStream(ctx, s.d.Objects, chunks, ext.Chunks)
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
			// The account's purge, the run's deletion or the deletion of
			// its logs took chunks; the purge and the deletion of the logs
			// delete the object too, and retention bounds it.
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
// one before ends, with Missing's line for a key that is empty; err is
// the first error an open or a read met, which the writer reading it may
// report otherwise.
type stream struct {
	ctx     context.Context //nolint:containedctx // an io.Reader's reads take none
	objects Objects
	keys    []string
	cur     io.ReadCloser
	err     error
}

// newStream is the stream of a state's n chunks, the indexed ones being
// chunks in sequence order, and its size.
func newStream(ctx context.Context, objects Objects, chunks []report.Chunk, n int64) (*stream,
	int64,
) {
	if k := len(chunks); k > 0 {
		n = max(n, chunks[k-1].Seq+1)
	}
	s := &stream{ctx: ctx, objects: objects, keys: make([]string, n)}
	size := int64(len(Missing)+1) * n
	for _, c := range chunks {
		if !c.Lost {
			s.keys[c.Seq] = c.Key
			size += c.Bytes - int64(len(Missing)+1)
		}
	}
	return s, size
}

func (s *stream) Read(p []byte) (int, error) {
	for {
		if s.cur == nil {
			if len(s.keys) == 0 {
				return 0, io.EOF
			}
			if s.keys[0] == "" {
				s.cur, s.keys = io.NopCloser(strings.NewReader(Missing+"\n")), s.keys[1:]
				continue
			}
			r, err := s.objects.Open(s.ctx, objectstore.RunLogs, s.keys[0])
			if err != nil {
				s.err = err
				return 0, err //nolint:wrapcheck // blobs names the object
			}
			s.cur, s.keys = r, s.keys[1:]
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
