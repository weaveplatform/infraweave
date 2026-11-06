package fs

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
	"github.com/weaveplatform/infraweave/internal/platform/objectstore"
)

// tokenVersion opens a token: 0x01 | key ID (uint32) | method (G or P) |
// expiry (Unix seconds, int64) | size (int64) | SHA-256 length (0 or 32)
// | SHA-256 | object key, then HMAC-SHA256 of all of it under the
// objectstore-url key the key ID names, all big-endian.
const tokenVersion byte = 0x01

const tokenHeader = 1 + 4 + 1 + 8 + 8 + 1

// ErrToken reports a token that is malformed, altered, expired or for
// another method.
var ErrToken = errors.New("invalid signed URL")

type token struct {
	method  byte
	expires int64
	size    int64
	sha256  []byte
	key     string
}

func methodByte(method string) byte {
	if method == http.MethodPut {
		return 'P'
	}
	return 'G'
}

// SignURL returns <url>/blobs/<token>, which needs no header. A PUT URL
// binds c.Size and, when given, c.SHA256.
func (b *Bucket) SignURL(
	_ context.Context,
	method, key string,
	ttl time.Duration,
	c objectstore.Constraints,
) (objectstore.Signed, error) {
	if method != http.MethodGet && method != http.MethodPut {
		return objectstore.Signed{}, fmt.Errorf("%w: %s", objectstore.ErrMethod, method)
	}
	if len(c.SHA256) != 0 && len(c.SHA256) != sha256.Size {
		return objectstore.Signed{}, fmt.Errorf("%w: a SHA-256 is 32 bytes", ErrToken)
	}
	mac, id, err := b.keys.DeriveKey(Purpose)
	if err != nil {
		return objectstore.Signed{}, fmt.Errorf("fs: %w", err)
	}
	t := make([]byte, 0, tokenHeader+len(c.SHA256)+len(key)+sha256.Size)
	t = append(t, tokenVersion)
	t = binary.BigEndian.AppendUint32(t, id)
	t = append(t, methodByte(method))
	expiry := b.now().Add(ttl).Unix()
	t = binary.BigEndian.AppendUint64(t, uint64(expiry)) //nolint:gosec // two's complement
	t = binary.BigEndian.AppendUint64(t, uint64(c.Size)) //nolint:gosec // as above
	t = append(t, byte(len(c.SHA256)))                   //nolint:gosec // 0 or 32, checked above
	t = append(t, c.SHA256...)
	t = append(t, key...)
	h := hmac.New(sha256.New, mac)
	h.Write(t)
	t = h.Sum(t)
	return objectstore.Signed{
		URL:    b.url + "/blobs/" + base64.RawURLEncoding.EncodeToString(t),
		Header: http.Header{},
	}, nil
}

// verify decodes s and checks its MAC, method and expiry.
func (b *Bucket) verify(s, method string) (token, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) < tokenHeader+sha256.Size || raw[0] != tokenVersion {
		return token{}, ErrToken
	}
	body, sum := raw[:len(raw)-sha256.Size], raw[len(raw)-sha256.Size:]
	mac, err := b.keys.DerivedKey(Purpose, binary.BigEndian.Uint32(body[1:]))
	if err != nil {
		return token{}, ErrToken
	}
	h := hmac.New(sha256.New, mac)
	h.Write(body)
	if !hmac.Equal(h.Sum(nil), sum) {
		return token{}, ErrToken
	}
	t := token{
		method:  body[5],
		expires: int64(binary.BigEndian.Uint64(body[6:])),  //nolint:gosec // two's complement
		size:    int64(binary.BigEndian.Uint64(body[14:])), //nolint:gosec // as above
	}
	n := int(body[22])
	if len(body) < tokenHeader+n {
		return token{}, ErrToken
	}
	t.sha256 = body[tokenHeader : tokenHeader+n]
	t.key = string(body[tokenHeader+n:])
	switch {
	case t.method != methodByte(method):
		return token{}, fmt.Errorf("%w: issued for another method", ErrToken)
	case b.now().Unix() >= t.expires:
		return token{}, fmt.Errorf("%w: expired", ErrToken)
	}
	return t, nil
}

// Routes adds GET and PUT /blobs/{token} to r, the API listener's router.
// They answer 403 for a token that does not verify, and a PUT whose
// Content-Length is not the token's size 413 when larger and 400 when
// smaller or missing.
func (b *Bucket) Routes(r chi.Router) {
	r.Get("/blobs/{token}", b.serveGet)
	r.Put("/blobs/{token}", b.servePut)
}

func (b *Bucket) serveGet(w http.ResponseWriter, r *http.Request) {
	t, err := b.verify(chi.URLParam(r, "token"), http.MethodGet)
	if err != nil {
		problem.Write(w, problem.Forbidden.New(err.Error()))
		return
	}
	size, err := b.Head(r.Context(), t.key)
	if errors.Is(err, objectstore.ErrNotFound) {
		problem.Write(w, problem.NotFound.New("no object at this URL"))
		return
	}
	body, err := b.Get(r.Context(), t.key)
	if err != nil {
		problem.Failure(w, err)
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	_, _ = io.Copy(w, body)
}

func (b *Bucket) servePut(w http.ResponseWriter, r *http.Request) {
	t, err := b.verify(chi.URLParam(r, "token"), http.MethodPut)
	if err != nil {
		problem.Write(w, problem.Forbidden.New(err.Error()))
		return
	}
	switch {
	case r.ContentLength > t.size:
		problem.Write(w, problem.PayloadTooLarge.New(
			fmt.Sprintf("this URL takes %d bytes", t.size)))
		return
	case r.ContentLength != t.size:
		problem.Write(w, problem.InvalidRequest.New(
			fmt.Sprintf("Content-Length must be %d", t.size)))
		return
	}
	var want []byte
	if len(t.sha256) != 0 {
		want = t.sha256
	}
	_, err = b.put(r.Context(), t.key, http.MaxBytesReader(w, r.Body, t.size), t.size, want)
	switch {
	case errors.Is(err, ErrChecksum):
		problem.Write(
			w,
			problem.ValidationFailed.New("the body's SHA-256 is not the one this URL takes"),
		)
	case errors.Is(err, objectstore.ErrSize):
		problem.Write(w, problem.InvalidRequest.New(err.Error()))
	case err != nil:
		problem.Failure(w, err)
	default:
		w.WriteHeader(http.StatusOK)
	}
}
