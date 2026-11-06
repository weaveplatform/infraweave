package seal_test

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/seal"
)

// key is a fixed 32-byte key whose bytes count up from start.
func key(start byte) []byte {
	b := make([]byte, seal.KeySize)
	for i := range b {
		b[i] = start + byte(i)
	}
	return b
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The known-answer vectors were sealed once with the keys beside them. A
// change to a format fails these before it reaches a stored value.
const (
	columnVector = "01000000076de2a1d735edb4e02004651259f55a0083e9d7d0582849a1cf58002373" +
		"60319e3984041b33fd92e2ad79d1ae78b1619d5836e8e0"
	objectVector = "49575342017072a4f6f0912c185afb38f71d78a062656b781dad4a56456d9b6007e2" +
		"edbee500f14690615a0dee34e13d617a0294a090ed3c2f"
	envelopeVector = "01046e986c5490174328e38f656b73533275750e15950f2bd04dfe95723d1afce5c8" +
		"4daa61ce668d63810b4e9e09056083f505fa2f947f40b90dd87694667855465235e8" +
		"493892e624f326fcecdc440814bfd7e9a0390d08cdec23c5140efb0a25b6d3260f0a" +
		"b88f0b57e86a2c27d37ce3800d1e4815e124"
	vectorAAD   = "config_elements.value_sealed:01JC0ACCOUNT/root/TOKEN"
	vectorRunID = "01JC0RUN0000000000000000AB"
)

func TestUnitKnownAnswers(t *testing.T) {
	got, err := seal.OpenColumn(key(0), []byte(vectorAAD), mustHex(t, columnVector))
	if err != nil || string(got) != "infraweave column vector" {
		t.Errorf("column: %q, %v", got, err)
	}
	if id, err := seal.ColumnKeyID(mustHex(t, columnVector)); err != nil || id != 7 {
		t.Errorf("column key ID: %d, %v", id, err)
	}
	r, err := seal.NewReader(bytes.NewReader(mustHex(t, objectVector)), key(0x20))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := io.ReadAll(r); err != nil || string(got) != "infraweave object vector" {
		t.Errorf("object: %q, %v", got, err)
	}
	pool, err := ecdh.P256().NewPrivateKey(key(1))
	if err != nil {
		t.Fatal(err)
	}
	got, err = seal.OpenEnvelope(pool, vectorRunID, 3, mustHex(t, envelopeVector))
	if err != nil || string(got) != "infraweave envelope vector" {
		t.Errorf("envelope: %q, %v", got, err)
	}
}

func TestUnitColumn(t *testing.T) {
	aad := []byte("idempotency_keys.body_sealed:01JC0ACCOUNT/key-1")
	sealed, err := seal.SealColumn(key(0), 42, aad, []byte("s3cr3t"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := seal.OpenColumn(key(0), aad, sealed); err != nil || string(got) != "s3cr3t" {
		t.Fatalf("round trip: %q, %v", got, err)
	}
	if id, err := seal.ColumnKeyID(sealed); err != nil || id != 42 {
		t.Errorf("key ID: %d, %v", id, err)
	}
	empty, err := seal.SealColumn(key(0), 1, aad, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := seal.OpenColumn(key(0), aad, empty); err != nil || len(got) != 0 {
		t.Errorf("empty value: %q, %v", got, err)
	}
	flipped := bytes.Clone(sealed)
	flipped[len(flipped)-1] ^= 1
	version := bytes.Clone(sealed)
	version[0] = 0x02
	for name, tc := range map[string]struct {
		key, aad, sealed []byte
		want             error
	}{
		"wrong key":         {key(1), aad, sealed, seal.ErrAuth},
		"changed AAD":       {key(0), []byte("idempotency_keys.body_sealed:01JC0ACCOUNT/key-2"), sealed, seal.ErrAuth},
		"another column":    {key(0), []byte("idempotency_keys.headers_sealed:01JC0ACCOUNT/key-1"), sealed, seal.ErrAuth},
		"flipped byte":      {key(0), aad, flipped, seal.ErrAuth},
		"truncated":         {key(0), aad, sealed[:len(sealed)-1], seal.ErrAuth},
		"header only":       {key(0), aad, sealed[:5], seal.ErrFormat},
		"empty":             {key(0), aad, nil, seal.ErrFormat},
		"unknown version":   {key(0), aad, version, seal.ErrVersion},
		"short key":         {key(0)[:16], aad, sealed, seal.ErrKeySize},
		"another row's key": {key(0), []byte("idempotency_keys.body_sealed:01JC0OTHER/key-1"), sealed, seal.ErrAuth},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := seal.OpenColumn(tc.key, tc.aad, tc.sealed); !errors.Is(err, tc.want) {
				t.Errorf("got %v, want %v", err, tc.want)
			}
		})
	}
	if _, err := seal.SealColumn(key(0)[:31], 1, aad, nil); !errors.Is(err, seal.ErrKeySize) {
		t.Errorf("sealing with a short key: %v", err)
	}
}

// sealObject seals plain in writes of chunk bytes.
func sealObject(t *testing.T, k, plain []byte, chunk int) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := seal.NewWriter(&buf, k)
	if err != nil {
		t.Fatal(err)
	}
	for p := plain; len(p) > 0; {
		n := min(chunk, len(p))
		if m, err := w.Write(p[:n]); err != nil || m != n {
			t.Fatalf("write: %d, %v", m, err)
		}
		p = p[n:]
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func openObject(k, sealed []byte) ([]byte, error) {
	r, err := seal.NewReader(bytes.NewReader(sealed), k)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

func TestUnitObjectRoundTrip(t *testing.T) {
	const seg = seal.SegmentSize
	for _, size := range []int{0, 1, seg - 1, seg, seg + 1, 2 * seg, 3*seg + 5} {
		plain := make([]byte, size)
		_, _ = rand.Read(plain)
		for _, chunk := range []int{1000, seg, size + 1} {
			sealed := sealObject(t, key(0), plain, chunk)
			segments := max(1, (size+seg-1)/seg)
			if want := 5 + size + 28*segments; len(sealed) != want ||
				seal.ObjectSize(int64(size)) != int64(want) {
				t.Errorf(
					"%d bytes in writes of %d: sealed to %d bytes, ObjectSize %d, want %d",
					size,
					chunk,
					len(sealed),
					seal.ObjectSize(int64(size)),
					want,
				)
			}
			got, err := openObject(key(0), sealed)
			if err != nil || !bytes.Equal(got, plain) {
				t.Errorf("%d bytes in writes of %d: %d bytes back, %v", size, chunk, len(got), err)
			}
		}
	}
}

func TestUnitObjectTampering(t *testing.T) {
	const seg = seal.SegmentSize
	const wire = seg + 28
	plain := make([]byte, 2*seg+100)
	_, _ = rand.Read(plain)
	sealed := sealObject(t, key(0), plain, seg)
	head, s0, s1, s2 := sealed[:5], sealed[5:5+wire], sealed[5+wire:5+2*wire], sealed[5+2*wire:]
	join := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	other := sealObject(t, key(0), plain[:100], seg)
	flipped := bytes.Clone(sealed)
	flipped[5+wire+40] ^= 1
	for name, tc := range map[string]struct {
		sealed []byte
		key    []byte
		want   error
	}{
		// The full middle segment, now last, was sealed with the flag 0.
		"dropped last segment": {join(head, s0, s1), key(0), seal.ErrAuth},
		"swapped segments":     {join(head, s1, s0, s2), key(0), seal.ErrAuth},
		// The last segment, now followed by another, was sealed with the
		// flag 1.
		"appended segment":  {join(head, s0, s1, s2, other[5:]), key(0), seal.ErrAuth},
		"dropped segment":   {join(head, s0, s2), key(0), seal.ErrAuth},
		"flipped byte":      {flipped, key(0), seal.ErrAuth},
		"wrong key":         {sealed, key(1), seal.ErrAuth},
		"cut mid-tag":       {sealed[:len(sealed)-1], key(0), seal.ErrAuth},
		"stub segment":      {join(head, s0[:10]), key(0), seal.ErrFormat},
		"no segments":       {head, key(0), seal.ErrFormat},
		"short header":      {head[:3], key(0), seal.ErrFormat},
		"wrong magic":       {join([]byte("IWSC\x01"), s0), key(0), seal.ErrFormat},
		"unknown version":   {join([]byte("IWSB\x02"), s0), key(0), seal.ErrVersion},
		"short key":         {sealed, key(0)[:16], seal.ErrKeySize},
		"segment past 2^32": {sealed, key(0), seal.ErrFormat},
	} {
		t.Run(name, func(t *testing.T) {
			if name == "segment past 2^32" {
				seal.SetMaxSegments(t, 2)
			}
			got, err := openObject(tc.key, tc.sealed)
			if !errors.Is(err, tc.want) {
				t.Errorf("got %v, want %v", err, tc.want)
			}
			// No byte of a segment comes out before it authenticates.
			if len(got)%seg != 0 {
				t.Errorf("returned %d bytes, part of a segment", len(got))
			}
		})
	}
	r, err := seal.NewReader(bytes.NewReader(join(head, s1)), key(0))
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 10)
	_, first := r.Read(buf)
	if _, again := r.Read(buf); !errors.Is(first, seal.ErrAuth) || !errors.Is(again, seal.ErrAuth) {
		t.Errorf("a failed read is not sticky: %v, then %v", first, again)
	}
}

func TestUnitObjectLimit(t *testing.T) {
	seal.SetMaxSegments(t, 2)
	w, err := seal.NewWriter(io.Discard, key(0))
	if err != nil {
		t.Fatal(err)
	}
	// The second segment is sealed once a third begins, and the third
	// once a fourth does.
	if _, err := w.Write(make([]byte, 2*seal.SegmentSize+1)); err != nil {
		t.Fatalf("two segments: %v", err)
	}
	if _, err := w.Write(make([]byte, seal.SegmentSize)); !errors.Is(err, seal.ErrTooLarge) {
		t.Errorf("a third segment: %v", err)
	}
	if err := w.Close(); !errors.Is(err, seal.ErrTooLarge) {
		t.Errorf("close after the limit: %v", err)
	}
	if _, err := seal.NewWriter(io.Discard, key(0)[:8]); !errors.Is(err, seal.ErrKeySize) {
		t.Errorf("short key: %v", err)
	}
}

func TestUnitObjectClose(t *testing.T) {
	w, err := seal.NewWriter(io.Discard, key(0))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("second close: %v", err)
	}
	if _, err := w.Write([]byte("x")); !errors.Is(err, seal.ErrClosed) {
		t.Errorf("write after close: %v", err)
	}
}

var errIO = errors.New("disk on fire")

// failingWriter accepts n bytes, then fails.
type failingWriter struct{ n int }

func (f *failingWriter) Write(p []byte) (int, error) {
	if len(p) > f.n {
		return 0, errIO
	}
	f.n -= len(p)
	return len(p), nil
}

// failingReader returns r's bytes, then errIO instead of io.EOF.
type failingReader struct{ r io.Reader }

func (f failingReader) Read(p []byte) (int, error) {
	n, err := f.r.Read(p)
	if errors.Is(err, io.EOF) {
		return n, errIO
	}
	return n, err //nolint:wrapcheck // passes the reader's error through
}

func TestUnitObjectIOErrors(t *testing.T) {
	if _, err := seal.NewWriter(&failingWriter{}, key(0)); !errors.Is(err, errIO) {
		t.Errorf("header write: %v", err)
	}
	w, err := seal.NewWriter(&failingWriter{n: 5}, key(0))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); !errors.Is(err, errIO) {
		t.Errorf("segment write: %v", err)
	}
	if _, err := w.Write([]byte("x")); !errors.Is(err, errIO) {
		t.Errorf("write after a failed write: %v", err)
	}
	sealed := sealObject(t, key(0), make([]byte, seal.SegmentSize), seal.SegmentSize)
	for name, cut := range map[string]int{
		"header":        0,
		"segment start": 5,
		"mid-segment":   100,
		"read-ahead":    len(sealed),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := openObjectFrom(failingReader{bytes.NewReader(sealed[:cut])}, key(0))
			if !errors.Is(err, errIO) {
				t.Errorf("got %v", err)
			}
		})
	}
}

func openObjectFrom(r io.Reader, k []byte) ([]byte, error) {
	sr, err := seal.NewReader(r, k)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(sr)
}

// pattern is a reader of n bytes whose byte i is byte(i*7), so a reader
// at the far end can check every byte without holding the object.
type pattern struct{ off, n int64 }

func (p *pattern) Read(b []byte) (int, error) {
	if p.off == p.n {
		return 0, io.EOF
	}
	b = b[:min(int64(len(b)), p.n-p.off)]
	for i := range b {
		b[i] = byte((p.off + int64(i)) * 7)
	}
	p.off += int64(len(b))
	return len(b), nil
}

// check is a writer that compares what it is given with pattern.
type check struct {
	off int64
	bad bool
}

func (c *check) Write(b []byte) (int, error) {
	for i, v := range b {
		if v != byte((c.off+int64(i))*7) {
			c.bad = true
		}
	}
	c.off += int64(len(b))
	return len(b), nil
}

// TestUnitObjectGiB streams 1 GiB through a Writer and a Reader joined by
// a pipe, so neither side could hold the object, and checks that the
// round trip allocates about a segment, not the object.
func TestUnitObjectGiB(t *testing.T) {
	if testing.Short() {
		t.Skip("streams 1 GiB")
	}
	const size = 1 << 30
	pr, pw := io.Pipe()
	src := make([]byte, 1<<20)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	done := make(chan error, 1)
	go func() {
		w, err := seal.NewWriter(pw, key(0))
		if err == nil {
			_, err = io.CopyBuffer(w, &pattern{n: size}, src)
		}
		if err == nil {
			err = w.Close()
		}
		pw.CloseWithError(err)
		done <- err
	}()
	r, err := seal.NewReader(pr, key(0))
	if err != nil {
		t.Fatal(err)
	}
	c := &check{}
	if _, err := io.CopyBuffer(c, r, make([]byte, 1<<20)); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if c.off != size || c.bad {
		t.Fatalf("read back %d bytes, mismatched %v", c.off, c.bad)
	}
	// Two segment buffers on each side and the copy buffers: a few MiB,
	// against the 1 GiB a buffered object would take.
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 8<<20 {
		t.Errorf("allocated %d bytes streaming 1 GiB", alloc)
	}
}

func TestUnitEnvelope(t *testing.T) {
	pool, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := seal.SealEnvelope(pool.PublicKey(), "01JC0RUN", 4, []byte("spec"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := seal.OpenEnvelope(
		pool,
		"01JC0RUN",
		4,
		sealed,
	); err != nil ||
		string(got) != "spec" {
		t.Fatalf("round trip: %q, %v", got, err)
	}
	version := bytes.Clone(sealed)
	version[0] = 0x02
	badPoint := bytes.Clone(sealed)
	badPoint[1] = 0x05
	for name, tc := range map[string]struct {
		pool         *ecdh.PrivateKey
		run          string
		stateVersion int64
		sealed       []byte
		want         error
	}{
		"another pool key":      {other, "01JC0RUN", 4, sealed, seal.ErrAuth},
		"another run":           {pool, "01JC0RUM", 4, sealed, seal.ErrAuth},
		"another state version": {pool, "01JC0RUN", 5, sealed, seal.ErrAuth},
		"unknown version":       {pool, "01JC0RUN", 4, version, seal.ErrVersion},
		"truncated":             {pool, "01JC0RUN", 4, sealed[:60], seal.ErrFormat},
		"empty":                 {pool, "01JC0RUN", 4, nil, seal.ErrFormat},
		"not a point":           {pool, "01JC0RUN", 4, badPoint, seal.ErrFormat},
		"run ID too long":       {pool, strings.Repeat("r", 1<<16), 4, sealed, seal.ErrFormat},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := seal.OpenEnvelope(
				tc.pool,
				tc.run,
				tc.stateVersion,
				tc.sealed,
			); !errors.Is(
				err,
				tc.want,
			) {
				t.Errorf("got %v, want %v", err, tc.want)
			}
		})
	}
	x25519, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seal.SealEnvelope(
		x25519.PublicKey(),
		"r",
		1,
		nil,
	); !errors.Is(
		err,
		seal.ErrPoolKey,
	) {
		t.Errorf("sealing to an X25519 key: %v", err)
	}
	if _, err := seal.OpenEnvelope(x25519, "r", 1, sealed); !errors.Is(err, seal.ErrPoolKey) {
		t.Errorf("opening with an X25519 key: %v", err)
	}
	if _, err := seal.SealEnvelope(
		pool.PublicKey(),
		strings.Repeat("r", 1<<16),
		1,
		nil,
	); !errors.Is(
		err,
		seal.ErrFormat,
	) {
		t.Errorf("sealing for an overlong run ID: %v", err)
	}
}
