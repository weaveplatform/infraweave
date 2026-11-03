// Package ratelimit enforces decision 0003's rate limits: one token bucket
// per caller and cost class, the bucket's state in X-RateLimit-* headers on
// every response, 429 when it is empty, and the operator's switch that turns
// the API off for one caller.
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/weaveplatform/infraweave/internal/server/problem"
)

// Class is an operation's cost class, its x-infraweave-cost in the deploy
// API.
type Class string

const (
	// Standard is reads and ordinary writes, and any operation that names no
	// class.
	Standard Class = "standard"
	// Expensive is search, batch operations, run triggers and other fan-out
	// work.
	Expensive Class = "expensive"
)

// Limit is one class's bucket: it holds up to Burst tokens and refills at
// Rate tokens a second.
type Limit struct {
	Rate  float64 `mapstructure:"rate"`
	Burst int     `mapstructure:"burst"`
}

// Config holds the server.rate_limit settings.
type Config struct {
	Standard  Limit `mapstructure:"standard"`
	Expensive Limit `mapstructure:"expensive"`
	// DisabledCallers are the callers the API answers 503 api-disabled.
	DisabledCallers []string `mapstructure:"disabled_callers"`
}

// Defaults give expensive work a tenth of the standard budget, so a caller
// running searches in a loop is throttled long before its reads are.
func Defaults() Config {
	return Config{
		Standard:  Limit{Rate: 10, Burst: 100},
		Expensive: Limit{Rate: 1, Burst: 10},
	}
}

var errLimit = errors.New("rate must be positive and burst at least 1")

// Validate rejects a bucket that could never admit a request or never
// refill. Errors name the key below server.rate_limit.
func (c Config) Validate() error {
	var errs []error
	for _, l := range []struct {
		class Class
		limit Limit
	}{{Standard, c.Standard}, {Expensive, c.Expensive}} {
		// Not Rate <= 0: a NaN rate must fail too.
		if !(l.limit.Rate > 0) || l.limit.Burst < 1 || math.IsInf(l.limit.Rate, 0) {
			errs = append(errs, fmt.Errorf("%s: %w, got rate %v and burst %d",
				l.class, errLimit, l.limit.Rate, l.limit.Burst))
		}
	}
	return errors.Join(errs...)
}

func (c Config) limit(class Class) Limit {
	if class == Expensive {
		return c.Expensive
	}
	return c.Standard
}

// CallerFunc names the caller a request counts against.
type CallerFunc func(*http.Request) string

// ClientAddr is the caller until authentication arrives: the client's IP
// address, without its port, so every connection from one host shares one
// budget. Behind a proxy that is the proxy's address; forwarded headers are
// not trusted because any client can send them.
func ClientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ClassFunc is the cost class of the operation a request is routed to. A
// class other than Expensive, the empty one included, counts as Standard.
type ClassFunc func(*http.Request) Class

// Middleware draws one token from the caller's bucket for the request's
// class, writes the bucket's state on the response and answers 429 when the
// bucket is empty. now is the clock the buckets refill by.
func Middleware(
	c Config,
	store Store,
	caller CallerFunc,
	class ClassFunc,
	now func() time.Time,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cl := class(r)
			if cl != Expensive {
				cl = Standard
			}
			l := c.limit(cl)
			st, err := store.Take(r.Context(), Key{Caller: caller(r), Class: cl}, l, now())
			if err != nil {
				problem.Internal(w)
				return
			}
			h := w.Header()
			h.Set("X-RateLimit-Limit", strconv.Itoa(l.Burst))
			h.Set("X-RateLimit-Remaining", strconv.Itoa(st.Remaining))
			h.Set("X-RateLimit-Reset", seconds(st.Reset))
			if !st.Allowed {
				h.Set("Retry-After", seconds(st.RetryAfter))
				problem.Write(w, problem.RateLimited.New(fmt.Sprintf(
					"The %s budget of %d requests is spent; retry after %s seconds.",
					cl, l.Burst, seconds(st.RetryAfter))))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// seconds rounds up, so a client that waits the number it is given never
// arrives early.
func seconds(d time.Duration) string {
	return strconv.FormatInt(int64(math.Ceil(d.Seconds())), 10)
}

// Switch is the operator's per-caller API switch.
type Switch interface {
	// Disabled reports whether the API is off for caller.
	Disabled(ctx context.Context, caller string) (bool, error)
}

// Callers is a Switch that disables a fixed set of callers.
type Callers map[string]struct{}

// NewCallers disables each of callers.
func NewCallers(callers []string) Callers {
	s := make(Callers, len(callers))
	for _, c := range callers {
		s[c] = struct{}{}
	}
	return s
}

// Disabled reports whether caller is in the set.
func (s Callers) Disabled(_ context.Context, caller string) (bool, error) {
	_, ok := s[caller]
	return ok, nil
}

// disabledRetryAfter is the Retry-After of api-disabled. The switch has no
// end time, so the value only spaces out a client's retries; five minutes
// keeps a retry loop from becoming the load the switch was turned on to shed.
const disabledRetryAfter = 5 * time.Minute

// Disabled answers 503 api-disabled to a caller sw has switched off, before
// the request reaches a bucket or a handler.
func Disabled(sw Switch, caller CallerFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			off, err := sw.Disabled(r.Context(), caller(r))
			if err != nil {
				problem.Internal(w)
				return
			}
			if off {
				w.Header().Set("Retry-After", seconds(disabledRetryAfter))
				problem.Write(w, problem.APIDisabled.New(
					"The operator has disabled the API for this caller."))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
