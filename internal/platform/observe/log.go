package observe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/weaveplatform/infraweave/internal/platform/buildinfo"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/middleware"
)

// LogConfig holds the log settings, log.format and log.level.
type LogConfig struct {
	// Format is json, one object per line, or text for local work.
	Format string `mapstructure:"format"`
	// Level is debug, info, warn or error.
	Level string `mapstructure:"level"`
}

// LogDefaults log JSON at info.
func LogDefaults() LogConfig { return LogConfig{Format: "json", Level: "info"} }

// ErrLogConfig marks an invalid log setting.
var ErrLogConfig = errors.New("invalid log configuration")

// Validate rejects a format or level the logger does not know.
func (c LogConfig) Validate() error {
	var errs []error
	if c.Format != "json" && c.Format != "text" {
		errs = append(
			errs,
			fmt.Errorf("%w: log.format must be json or text, got %q", ErrLogConfig, c.Format),
		)
	}
	if _, err := ParseLevel(c.Level); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// ParseLevel reads a log.level value.
func ParseLevel(s string) (slog.Level, error) {
	switch s {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf(
		"%w: log.level must be debug, info, warn or error, got %q",
		ErrLogConfig,
		s,
	)
}

// Service is the service field of every line.
const Service = "infraweave"

// NewLogger writes c's format to w at the level lv holds, which starts at
// c's level. Every line carries the standard fields: time in UTC, level,
// msg, service, mode, version and, inside a request, request_id; every
// attribute passes through Redact first. mode is the process mode, such as
// serve or serve,scheduler for infraweave all.
//
// ref: decision 0017
func NewLogger(w io.Writer, c LogConfig, mode string) (*slog.Logger, *slog.LevelVar, error) {
	if err := c.Validate(); err != nil {
		return nil, nil, err
	}
	level, _ := ParseLevel(c.Level) // valid: Validate passed
	lv := new(slog.LevelVar)
	lv.Set(level)
	opts := &slog.HandlerOptions{Level: lv, ReplaceAttr: utc}
	var h slog.Handler = slog.NewJSONHandler(w, opts)
	if c.Format == "text" {
		h = slog.NewTextHandler(w, opts)
	}
	h = Redact(requestHandler{h}).WithAttrs([]slog.Attr{
		slog.String("service", Service),
		slog.String("mode", mode),
		slog.String("version", buildinfo.Version()),
	})
	return slog.New(h), lv, nil
}

// utc writes the record's time in UTC.
func utc(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 && a.Key == slog.TimeKey && a.Value.Kind() == slog.KindTime {
		a.Value = slog.TimeValue(a.Value.Time().UTC())
	}
	return a
}

// requestHandler adds the request ID of the record's context, unless the
// record names one itself.
type requestHandler struct{ slog.Handler }

func (h requestHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := middleware.RequestIDFrom(ctx); id != "" {
		named := false
		r.Attrs(func(a slog.Attr) bool {
			named = a.Key == "request_id"
			return !named
		})
		if !named {
			r.AddAttrs(slog.String("request_id", id))
		}
	}
	//nolint:wrapcheck // a handler's error is the caller's to ignore
	return h.Handler.Handle(ctx, r)
}

func (h requestHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return requestHandler{h.Handler.WithAttrs(as)}
}

func (h requestHandler) WithGroup(name string) slog.Handler {
	return requestHandler{h.Handler.WithGroup(name)}
}

// FollowLevel sets lv from read each time sig delivers, until ctx ends: the
// process re-reads log.level on SIGHUP and nothing else. A read that fails
// keeps the level and logs why.
func FollowLevel(
	ctx context.Context,
	sig <-chan os.Signal,
	read func() (slog.Level, error),
	lv *slog.LevelVar,
	log *slog.Logger,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-sig:
			level, err := read()
			if err != nil {
				log.ErrorContext(ctx, "log.level not reloaded", "error", err)
				continue
			}
			lv.Set(level)
			log.InfoContext(ctx, "log.level reloaded", "log_level", strings.ToLower(level.String()))
		}
	}
}

// warnEvery is how often one source's repeated warning is logged.
const warnEvery = time.Minute

// Warnings logs a warning from one source, such as an unreachable endpoint,
// at most once a minute, with the count of the ones it held back, so a
// failure that repeats every request does not flood the log.
type Warnings struct {
	log *slog.Logger
	now func() time.Time

	mu      sync.Mutex
	sources map[string]*warned
}

type warned struct {
	at         time.Time
	suppressed int
}

// NewWarnings logs through log by the clock now.
func NewWarnings(log *slog.Logger, now func() time.Time) *Warnings {
	return &Warnings{log: log, now: now, sources: map[string]*warned{}}
}

// Warn logs msg with args for source, unless source logged within the last
// minute, in which case it is counted and the next one that is logged
// carries the count as suppressed.
func (w *Warnings) Warn(ctx context.Context, source, msg string, args ...any) {
	w.mu.Lock()
	now := w.now()
	s, ok := w.sources[source]
	if ok && now.Sub(s.at) < warnEvery {
		s.suppressed++
		w.mu.Unlock()
		return
	}
	suppressed := 0
	if ok {
		suppressed = s.suppressed
	}
	w.sources[source] = &warned{at: now}
	w.mu.Unlock()
	w.log.WarnContext(ctx, msg, append(args, "source", source, "suppressed", suppressed)...)
}
