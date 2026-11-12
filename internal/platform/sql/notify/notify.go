package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// The channels, each with the payload it carries.
//
// ref: decision 0005
const (
	// Jobs carries a queue name and wakes idle queue workers.
	// ref: decision 0011
	Jobs = "infraweave_jobs"
	// Runs carries a worker pool ID, which wakes the pool's held worker
	// polls, or a pool and run ID, which wakes one of them.
	// ref: decision 0008
	Runs = "infraweave_runs"
	// VCSAgent carries a VCS agent pool ID or a request ID.
	// ref: decision 0012
	VCSAgent = "infraweave_vcs_agent"
	// Invalidate carries a cache key and clears in-process caches.
	Invalidate = "infraweave_invalidate"
	// Events carries an event ID and wakes server-sent-event streams.
	// ref: decision 0015
	Events = "infraweave_events"
	// Logs carries a run ID, state and state version and wakes run log
	// streams.
	// ref: decision 0015
	Logs = "infraweave_logs"
)

// Channels are every channel the Listener listens on. The set is fixed, so
// the connection issues its LISTENs once per connect and never between two
// waits.
var Channels = []string{Jobs, Runs, VCSAgent, Invalidate, Events, Logs}

// ApplicationName names the listening connection in pg_stat_activity.
const ApplicationName = "infraweave-listener"

// ErrChannel reports a channel outside Channels.
var ErrChannel = errors.New("not a notification channel")

// Message is one delivery to a subscriber: a notification's payload, or
// Resync. Resync arrives each time the connection is established, and
// whenever the subscriber fell so far behind that a notification was
// dropped, because notifications sent while nobody listened are lost: the
// subscriber re-checks whatever it waits on.
type Message struct {
	Payload string
	Resync  bool
}

// buffer is each subscriber's queue. A subscriber that lets it fill loses
// payloads but never the Resync that replaces them.
const buffer = 64

// Subscription delivers one channel's messages until Close.
type Subscription struct {
	C <-chan Message

	c       chan Message
	l       *Listener
	channel string
	// behind records a dropped message; the next delivery that fits is a
	// Resync.
	behind bool
}

// Close stops the deliveries. C is not closed, so a receiver selecting on
// it with other cases is never woken by Close.
func (s *Subscription) Close() {
	s.l.mu.Lock()
	defer s.l.mu.Unlock()
	delete(s.l.subs[s.channel], s)
}

// deliver runs under the listener's lock and never blocks.
func (s *Subscription) deliver(m Message) {
	if s.behind {
		select {
		case s.c <- Message{Resync: true}:
			s.behind = false
		default:
			return
		}
		if m.Resync {
			return
		}
	}
	select {
	case s.c <- m:
	default:
		s.behind = true
	}
}

// Listener holds the replica's one listening connection, outside the pool
// and so outside any transaction-mode pooler, and fans its notifications
// out to subscribers.
type Listener struct {
	cfg Config
	log *slog.Logger

	mu   sync.Mutex
	subs map[string]map[*Subscription]struct{}
	// listening is true from LISTEN on every channel until the connection
	// fails, the window in which no notification is lost.
	listening atomic.Bool
}

// Listening reports whether the listening connection is up, so a
// notification sent now reaches the subscribers. A readiness check of a
// process whose work waits on notifications reads it.
func (l *Listener) Listening() bool { return l.listening.Load() }

// backoff bounds the wait between reconnects: it starts at the first value
// and doubles to the second.
var backoff = [2]time.Duration{100 * time.Millisecond, 5 * time.Second}

// Config returns the settings of one new connection, such as platform/sql's
// DB.ConnConfig. The listener asks again before each reconnect, so a
// password that is a short-lived IAM token is fresh every time.
type Config func(ctx context.Context) (*pgx.ConnConfig, error)

// Static is the Config that always returns a copy of cfg.
func Static(cfg *pgx.ConnConfig) Config {
	return func(context.Context) (*pgx.ConnConfig, error) { return cfg.Copy(), nil }
}

// NewListener returns a listener that connects with cfg's settings once Run
// starts.
func NewListener(cfg Config, log *slog.Logger) *Listener {
	return &Listener{cfg: cfg, log: log, subs: map[string]map[*Subscription]struct{}{}}
}

// Subscribe delivers channel's notifications to the returned subscription.
func (l *Listener) Subscribe(channel string) (*Subscription, error) {
	if !slices.Contains(Channels, channel) {
		return nil, fmt.Errorf("%w: %q", ErrChannel, channel)
	}
	c := make(chan Message, buffer)
	s := &Subscription{C: c, c: c, l: l, channel: channel}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.subs[channel] == nil {
		l.subs[channel] = map[*Subscription]struct{}{}
	}
	l.subs[channel][s] = struct{}{}
	return s, nil
}

func (l *Listener) broadcast(channel string, m Message) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for ch, subs := range l.subs {
		if channel != "" && ch != channel {
			continue
		}
		for s := range subs {
			s.deliver(m)
		}
	}
}

// Run listens until ctx is done, reconnecting with backoff whenever the
// connection fails. Every subscriber gets a Resync after each connect.
func (l *Listener) Run(ctx context.Context) {
	wait := backoff[0]
	for {
		established, err := l.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if established {
			wait = backoff[0]
		}
		d := wait/2 + rand.N(wait/2+1) //nolint:gosec // jitter, not a secret
		l.log.Warn("notify: the listening connection failed; reconnecting",
			"error", err, "retry_in", d)
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		wait = min(wait*2, backoff[1])
	}
}

// session connects, listens on every channel and delivers until the
// connection fails or ctx is done. It reports whether it got as far as
// listening.
func (l *Listener) session(ctx context.Context) (bool, error) {
	cfg, err := l.cfg(ctx)
	if err != nil {
		return false, fmt.Errorf("connection settings: %w", err)
	}
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	cfg.RuntimeParams["application_name"] = ApplicationName
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return false, fmt.Errorf("connect: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()
	for _, ch := range Channels {
		if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{ch}.Sanitize()); err != nil {
			return false, fmt.Errorf("listen %s: %w", ch, err)
		}
	}
	l.listening.Store(true)
	defer l.listening.Store(false)
	l.broadcast("", Message{Resync: true})
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return true, fmt.Errorf("wait: %w", err)
		}
		l.broadcast(n.Channel, Message{Payload: n.Payload})
	}
}

// Execer runs a statement; a pool, a connection and a transaction are each
// one.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Notify sends payload on channel through q. Sent inside a transaction, it
// is delivered when the transaction commits, and never if it rolls back.
func Notify(ctx context.Context, q Execer, channel, payload string) error {
	if !slices.Contains(Channels, channel) {
		return fmt.Errorf("%w: %q", ErrChannel, channel)
	}
	if _, err := q.Exec(ctx, "SELECT pg_notify($1, $2)", channel, payload); err != nil {
		return fmt.Errorf("notify %s: %w", channel, err)
	}
	return nil
}
