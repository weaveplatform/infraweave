package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// Header is the request header that carries the key.
const Header = "Idempotency-Key"

// ReplayedHeader marks a replayed response.
const ReplayedHeader = "Idempotency-Replayed"

// MaxKeyLength is the longest key accepted, the IdempotencyKey parameter's
// maxLength in api/src/api/components-common.yaml.
const MaxKeyLength = 255

// MaxBody is the largest body fingerprinted, decision 0022's default for
// api.max_body_bytes. The body is held in memory to hash it before the
// handler reads it, so without a bound one request could exhaust memory.
const MaxBody = 1 << 20

// Config holds the server.idempotency settings.
type Config struct {
	// TTL is how long a key is kept.
	TTL time.Duration `mapstructure:"ttl"`
}

// Defaults keep a key for decision 0003's 24 hours.
func Defaults() Config { return Config{TTL: 24 * time.Hour} }

var errTTL = errors.New("ttl must be positive")

// Validate rejects a TTL under which no key would outlive its request.
// Errors name the key below server.idempotency.
func (c Config) Validate() error {
	if c.TTL <= 0 {
		return fmt.Errorf("%w, got %s", errTTL, c.TTL)
	}
	return nil
}

// Middleware applies the key to POST and PATCH requests that applies
// accepts; the server's applies excludes searches, which change nothing, and
// requests no operation serves. Every other request passes untouched, as
// does one without the header. caller scopes the keys.
func Middleware(
	store Store,
	caller func(*http.Request) string,
	applies func(*http.Request) bool,
	log *slog.Logger,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			keys := r.Header.Values(Header)
			if (r.Method != http.MethodPost && r.Method != http.MethodPatch) ||
				len(keys) == 0 || !applies(r) {
				next.ServeHTTP(w, r)
				return
			}
			g := guard{store: store, log: log}
			g.serve(w, r, next, keys, caller(r))
		})
	}
}

type guard struct {
	store Store
	log   *slog.Logger
}

func (g guard) serve(
	w http.ResponseWriter,
	r *http.Request,
	next http.Handler,
	keys []string,
	caller string,
) {
	// The generated handler binds header parameters without their schema's
	// length, so the bound is checked here.
	if len(keys) > 1 || keys[0] == "" || len(keys[0]) > MaxKeyLength {
		problem.Write(w, problem.InvalidRequest.New(fmt.Sprintf(
			"Send one %s of 1 to %d characters.", Header, MaxKeyLength)))
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBody+1))
	if err != nil {
		problem.Write(w, problem.InvalidRequest.New("The request body could not be read."))
		return
	}
	if len(body) > MaxBody {
		problem.Write(w, problem.PayloadTooLarge.New(fmt.Sprintf(
			"A request with an %s may carry at most %d bytes.", Header, MaxBody)))
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	fp := fingerprint(r, body)
	key := Key{Caller: caller, Value: keys[0]}

	rec, found, err := g.store.Get(r.Context(), key)
	if err != nil {
		g.fail(w, r, "get", err)
		return
	}
	if found {
		answer(w, rec, fp)
		return
	}
	reserved, err := g.store.Reserve(r.Context(), key, fp)
	if err != nil {
		g.fail(w, r, "reserve", err)
		return
	}
	if !reserved {
		// Another request took the key between Get and Reserve, so it is
		// in flight now.
		writeInFlight(w)
		return
	}
	g.run(w, r, next, key)
}

// answer replies to a request whose key already has a record.
func answer(w http.ResponseWriter, rec Record, fp string) {
	switch {
	case rec.Fingerprint != fp:
		problem.Write(w, problem.IdempotencyKeyReuse.New(
			"This "+Header+" was used with a different request."))
	case rec.Response == nil:
		writeInFlight(w)
	default:
		h := w.Header()
		if rec.Response.ContentType != "" {
			h.Set("Content-Type", rec.Response.ContentType)
		}
		if rec.Response.Location != "" {
			h.Set("Location", rec.Response.Location)
		}
		h.Set(ReplayedHeader, "true")
		w.WriteHeader(rec.Response.Status)
		_, _ = w.Write(rec.Response.Body)
	}
}

func writeInFlight(w http.ResponseWriter) {
	problem.Write(w, problem.IdempotencyKeyInFlight.New(
		"The first request with this "+Header+" has not finished; retry later."))
}

// run serves the request that holds key and stores its response, or
// releases the key when the response is a 5xx or the handler panics, so a
// retry runs the operation again.
func (g guard) run(w http.ResponseWriter, r *http.Request, next http.Handler, key Key) {
	// The client may be gone by the time the handler returns; the outcome
	// is recorded regardless.
	ctx := context.WithoutCancel(r.Context())
	cw := &capture{ResponseWriter: w}
	finished := false
	defer func() {
		if !finished {
			g.release(ctx, key)
		}
	}()
	next.ServeHTTP(cw, r)
	finished = true
	status := cw.status
	if status == 0 {
		status = http.StatusOK
	}
	if status >= http.StatusInternalServerError {
		g.release(ctx, key)
		return
	}
	err := g.store.Complete(ctx, key, Response{
		Status:      status,
		ContentType: w.Header().Get("Content-Type"),
		Location:    w.Header().Get("Location"),
		Body:        cw.body.Bytes(),
	})
	if err != nil {
		// The response has gone; the key stays in flight until it expires.
		g.log.ErrorContext(ctx, "idempotency store failed", "op", "complete", "error", err)
	}
}

func (g guard) release(ctx context.Context, key Key) {
	if err := g.store.Release(ctx, key); err != nil {
		g.log.ErrorContext(ctx, "idempotency store failed", "op", "release", "error", err)
	}
}

func (g guard) fail(w http.ResponseWriter, r *http.Request, op string, err error) {
	g.log.ErrorContext(r.Context(), "idempotency store failed", "op", op, "error", err)
	problem.Internal(w)
}

// fingerprint is decision 0003's identity of a request: its method, its path
// with the query string and a SHA-256 of its body.
func fingerprint(r *http.Request, body []byte) string {
	sum := sha256.Sum256(body)
	return r.Method + " " + r.URL.RequestURI() + " " + hex.EncodeToString(sum[:])
}

// capture passes a response through while keeping its status and body.
type capture struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (c *capture) WriteHeader(code int) {
	// 1xx responses are interim; the final status comes later.
	if c.status == 0 && code >= http.StatusOK {
		c.status = code
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *capture) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	c.body.Write(b)
	n, err := c.ResponseWriter.Write(b)
	if err != nil {
		return n, fmt.Errorf("write response: %w", err)
	}
	return n, nil
}

// Unwrap lets http.ResponseController reach the connection's writer.
func (c *capture) Unwrap() http.ResponseWriter { return c.ResponseWriter }
