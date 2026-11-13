package core

import (
	"bytes"
	"context"
	"crypto/fips140"
	"crypto/md5" //nolint:gosec // checks a body against its Content-MD5; the SHA-256 is recorded
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"slices"
	"strings"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
	"github.com/weaveplatform/infraweave/internal/platform/tx"
	stateapi "github.com/weaveplatform/infraweave/internal/state/api"
)

// DefaultMaxSize is state.max_size's default, the largest state version
// in bytes a write may carry.
//
// ref: decision 0010 § "Writing a version"
const DefaultMaxSize = 512 << 20

var (
	// ErrMD5 reports a body that does not match the Content-MD5 sent with
	// it.
	ErrMD5 = errors.New("state: the body does not match its Content-MD5")
	// ErrState reports a body that is no JSON object, or one with some but
	// not all of a plain state's version, serial and lineage.
	ErrState = errors.New("state: the body is not a state")
)

// Version is a state version as the store keeps it: its metadata and its
// object's key in the states store.
type Version struct {
	stateapi.Version
	Key string
}

// Write is a state version Run sends to its target's workspace: LockID
// is the ID of the lock it writes under, Body its Size bytes, and
// ContentMD5 the Content-MD5 header sent with it, empty for none.
type Write struct {
	Target
	Run        kernel.ULID
	LockID     string
	Body       io.Reader
	Size       int64
	ContentMD5 string
}

// Write stores w's body as the new current version of its workspace,
// sealed in its own object of the states store, and answers whether it
// added one: a body whose SHA-256 is the current version's adds none. It
// answers a *Conflict, storing nothing, when w's run does not hold the
// workspace's exclusive lock under w's ID, checked before the body
// streams and again as the version commits; ErrMD5 for a body that does
// not match its Content-MD5, which goes unchecked under fips140=only,
// where Go refuses MD5; and ErrState for a body that is no state. A
// stream that fails leaves its object pending for blob.expire.
//
// ref: decision 0010 § "Writing a version"
func (s *Service) Write(ctx context.Context, w Write) (bool, error) {
	held, err := s.store.Locks(ctx, w.Target)
	if err != nil {
		return false, err //nolint:wrapcheck // the store names the read
	}
	if c := writer(held, w); c != nil {
		return false, c
	}
	v := Version{Version: stateapi.Version{
		ID: kernel.NewULID(), Stack: w.Stack, Workspace: w.Workspace, Run: &w.Run,
	}}
	v.Key = fmt.Sprintf("%s/%s/%x/%s", w.Account, w.Stack, sha256.Sum256([]byte(w.Workspace)),
		v.ID)
	if err := s.put(ctx, w, &v); err != nil {
		return false, err
	}
	return s.commit(ctx, w, v)
}

// put streams w's body into v's object, recording its size, SHA-256 and
// what the body says of itself in v. A body that fails its Content-MD5 or
// is no state has its object deleted.
func (s *Service) put(ctx context.Context, w Write, v *Version) error {
	body := w.Body
	var sum hash.Hash
	if w.ContentMD5 != "" && !fips140.Enforced() {
		sum = md5.New() //nolint:gosec // checks a body against its Content-MD5; the SHA-256 is recorded
		body = io.TeeReader(body, sum)
	}
	sc := newScanner(body)
	obj, err := s.objects.Put(ctx, objectstore.States, v.Key, sc, w.Size)
	m, scanErr := sc.result(err)
	if err != nil {
		return err //nolint:wrapcheck // the objects name the key
	}
	if sum != nil {
		if sent, err := base64.StdEncoding.DecodeString(w.ContentMD5); err != nil ||
			!bytes.Equal(sent, sum.Sum(nil)) {
			scanErr = ErrMD5
		}
	}
	if scanErr != nil {
		return errors.Join(scanErr, tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
			return s.objects.Delete(ctx, objectstore.States, v.Key)
		}))
	}
	v.Size, v.SHA256 = obj.Size, obj.SHA256[:]
	v.Serial, v.Lineage, v.ClientEncrypted = m.serial, m.lineage, m.encrypted
	return nil
}

// commit makes v the current version of w's workspace in one
// transaction under its stack's hold and its workspace's row lock: the
// object goes live, the version row goes in and the workspace points at
// it. A run that no longer holds the lock, or a body the current version
// already has, has the object deleted instead.
func (s *Service) commit(ctx context.Context, w Write, v Version) (bool, error) {
	var (
		refused *Conflict
		added   bool
	)
	//nolint:wrapcheck // each step in the transaction names itself
	err := tx.Do(ctx, s.db, tx.Options{}, func(ctx context.Context) error {
		refused, added = nil, false
		if err := s.stacks.Hold(ctx, w.Account, w.Stack); err != nil {
			return err
		}
		if _, err := s.store.LockWorkspace(ctx, w.Target); err != nil {
			return err
		}
		held, err := s.store.Locks(ctx, w.Target)
		if err != nil {
			return err
		}
		cur, ok, err := s.store.Current(ctx, w.Target)
		if err != nil {
			return err
		}
		refused = writer(held, w)
		if refused != nil || (ok && bytes.Equal(cur.SHA256, v.SHA256)) {
			return s.objects.Delete(ctx, objectstore.States, v.Key)
		}
		if err := s.objects.Commit(ctx, objectstore.States, v.Key); err != nil {
			return err
		}
		added = true
		return s.store.AddVersion(ctx, w.Account, v)
	})
	switch {
	case err != nil:
		return false, err //nolint:wrapcheck // each step in the transaction names itself
	case refused != nil:
		return false, refused
	}
	return added, nil
}

// writer is the conflict that refuses w unless held has an exclusive lock
// of w's run with w's ID: it names the holder of held, or no lock when
// none is held.
func writer(held []Lock, w Write) *Conflict {
	for _, l := range held {
		if l.ID == w.LockID && l.Run == w.Run && l.Mode == Exclusive {
			return nil
		}
	}
	if len(held) == 0 {
		return &Conflict{Holder: Lock{Info: json.RawMessage(`{}`)}}
	}
	return &Conflict{Holder: holder(held)}
}

// Read opens the current version of t's workspace, false when the
// workspace has none or does not exist. The caller closes the contents,
// which stream unsealed.
//
// ref: decision 0010 § "Reading"
func (s *Service) Read(ctx context.Context, t Target) (Version, io.ReadCloser, bool, error) {
	v, ok, err := s.store.Current(ctx, t)
	if err != nil || !ok {
		return Version{}, nil, false, err //nolint:wrapcheck // the store names the read
	}
	r, err := s.objects.Open(ctx, objectstore.States, v.Key)
	if err != nil {
		return Version{}, nil, false, err //nolint:wrapcheck // the objects name the key
	}
	return v, r, true, nil
}

// Latest reads the metadata of the current version of acct's stack's
// workspace, false when the workspace has none.
func (s *Service) Latest(
	ctx context.Context,
	acct kernel.AccountID,
	stack kernel.ULID,
	workspace string,
) (stateapi.Version, bool, error) {
	v, ok, err := s.store.Current(ctx, Target{Account: acct, Stack: stack, Workspace: workspace})
	return v.Version, ok, err //nolint:wrapcheck // the store names the read
}

// Outputs reads the root module outputs of the current version of acct's
// stack's default workspace, by name: none when it has no version or the
// version is client-encrypted, whose body the server never parses.
func (s *Service) Outputs(
	ctx context.Context,
	acct kernel.AccountID,
	stack kernel.ULID,
) ([]stateapi.Output, error) {
	t := Target{Account: acct, Stack: stack, Workspace: stateapi.DefaultWorkspace}
	v, ok, err := s.store.Current(ctx, t)
	if err != nil || !ok || v.ClientEncrypted {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	r, err := s.objects.Open(ctx, objectstore.States, v.Key)
	if err != nil {
		return nil, err //nolint:wrapcheck // the objects name the key
	}
	defer r.Close()
	out, err := outputs(r)
	if err != nil {
		return nil, fmt.Errorf("state: read the outputs of version %s: %w", v.ID, err)
	}
	return out, nil
}

// outputs reads a state's outputs, decoding the outputs member alone so
// the rest of a large state streams past.
func outputs(r io.Reader) ([]stateapi.Output, error) {
	dec := json.NewDecoder(r)
	if err := open(dec); err != nil {
		return nil, err
	}
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return nil, err //nolint:wrapcheck // the decoder names the offset
		}
		if k != "outputs" {
			if err := skip(dec); err != nil {
				return nil, err
			}
			continue
		}
		var byName map[string]struct {
			Value     json.RawMessage `json:"value"`
			Sensitive bool            `json:"sensitive"`
		}
		if err := dec.Decode(&byName); err != nil {
			return nil, err //nolint:wrapcheck // as above
		}
		out := make([]stateapi.Output, 0, len(byName))
		for name, o := range byName {
			out = append(out, stateapi.Output{Name: name, Value: o.Value, Sensitive: o.Sensitive})
		}
		slices.SortFunc(
			out,
			func(a, b stateapi.Output) int { return strings.Compare(a.Name, b.Name) },
		)
		return out, nil
	}
	return nil, nil
}

// meta is what a state body says of itself: a plain state's serial and
// lineage, or that it is client-encrypted, as OpenTofu's encryption
// writes a state with neither.
type meta struct {
	serial    *int64
	lineage   *string
	encrypted bool
}

// scanner scans a body for its meta as the body streams to its object,
// through a pipe, so the body is read once and never held whole.
type scanner struct {
	io.Reader
	pw   *io.PipeWriter
	done chan scanned
}

type scanned struct {
	m   meta
	err error
}

func newScanner(body io.Reader) *scanner {
	pr, pw := io.Pipe()
	s := &scanner{Reader: io.TeeReader(body, pw), pw: pw, done: make(chan scanned, 1)}
	go func() {
		m, err := scan(pr)
		// The body streams on past a scan that ends early.
		_, _ = io.Copy(io.Discard, pr)
		s.done <- scanned{m, err}
	}()
	return s
}

// result ends the scan once the body has streamed, or has failed with
// err, and answers what the scan found.
func (s *scanner) result(err error) (meta, error) {
	s.pw.CloseWithError(err)
	r := <-s.done
	return r.m, r.err
}

// scan reads a body's top-level object, skipping every member but version,
// serial and lineage, through to its end.
func scan(r io.Reader) (meta, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	if err := open(dec); err != nil {
		return meta{}, err
	}
	var (
		m       meta
		version bool
	)
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return meta{}, notState(err)
		}
		switch k {
		case "serial":
			m.serial, err = serial(dec)
		case "lineage":
			m.lineage, err = lineage(dec)
		case "version":
			version = true
			err = skip(dec)
		default:
			err = skip(dec)
		}
		if err != nil {
			return meta{}, notState(err)
		}
	}
	if _, err := dec.Token(); err != nil {
		return meta{}, notState(err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return meta{}, fmt.Errorf("%w: data after the object", ErrState)
	}
	switch {
	case m.serial == nil && m.lineage == nil:
		m.encrypted = true
	case m.serial == nil || m.lineage == nil || !version:
		return meta{}, fmt.Errorf("%w: a plain state has version, serial and lineage", ErrState)
	}
	return m, nil
}

// open reads the brace that opens a body's top-level object.
func open(dec *json.Decoder) error {
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return fmt.Errorf("%w: not a JSON object", ErrState)
	}
	return nil
}

func notState(err error) error {
	if errors.Is(err, ErrState) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrState, err)
}

// skip reads past the value dec is at, one token at a time, so a member
// of any size streams past without being held.
func skip(dec *json.Decoder) error {
	depth := 0
	for {
		t, err := dec.Token()
		if err != nil {
			return err //nolint:wrapcheck // the decoder names the offset
		}
		switch t {
		case json.Delim('{'), json.Delim('['):
			depth++
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
		if depth == 0 {
			return nil
		}
	}
}

func serial(dec *json.Decoder) (*int64, error) {
	t, err := dec.Token()
	if err != nil {
		return nil, err //nolint:wrapcheck // the decoder names the offset
	}
	n, ok := t.(json.Number)
	if !ok {
		return nil, fmt.Errorf("%w: serial is not a number", ErrState)
	}
	i, err := n.Int64()
	if err != nil || i < 0 {
		return nil, fmt.Errorf("%w: serial %s is not a whole number", ErrState, n)
	}
	return &i, nil
}

func lineage(dec *json.Decoder) (*string, error) {
	t, err := dec.Token()
	if err != nil {
		return nil, err //nolint:wrapcheck // the decoder names the offset
	}
	s, ok := t.(string)
	if !ok {
		return nil, fmt.Errorf("%w: lineage is not a string", ErrState)
	}
	return &s, nil
}
