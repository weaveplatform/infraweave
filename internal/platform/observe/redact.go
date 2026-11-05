package observe

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
)

// Redacted replaces every value the log must not carry (decision 0013's
// marker).
const Redacted = "[redacted]"

// Secret is a string that is never logged: slog writes it as Redacted under
// any key, and so does fmt. Reveal returns the value for the one place that
// uses it.
type Secret string

// LogValue implements slog.LogValuer.
func (Secret) LogValue() slog.Value { return slog.StringValue(Redacted) }

// String implements fmt.Stringer.
func (Secret) String() string { return Redacted }

// Reveal is the secret itself.
func (s Secret) Reveal() string { return string(s) }

// secretKeys are the attribute keys whose values are replaced, case-folded;
// a key ending in one of secretSuffixes is too.
var (
	secretKeys = map[string]bool{
		"authorization": true, "cookie": true, "set-cookie": true, "password": true,
		"secret": true, "token": true, "private_key": true, "client_secret": true,
	}
	secretSuffixes = []string{"_secret", "_token", "_password"}
)

// loggedHeaders are the only headers a logged http.Header keeps.
var loggedHeaders = []string{"Content-Type", "User-Agent", "X-Request-Id", "Idempotency-Key"}

func secretKey(key string) bool {
	k := strings.ToLower(key)
	if secretKeys[k] {
		return true
	}
	for _, s := range secretSuffixes {
		if strings.HasSuffix(k, s) {
			return true
		}
	}
	return false
}

// Redact wraps next so that no attribute reaches it unredacted: a value
// under a secret key is replaced by Redacted, an http.Header keeps only the
// allowlisted headers, groups are redacted throughout, and a Secret is
// Redacted by its LogValue.
func Redact(next slog.Handler) slog.Handler { return redact{next} }

type redact struct{ next slog.Handler }

func (h redact) Enabled(ctx context.Context, l slog.Level) bool { return h.next.Enabled(ctx, l) }

func (h redact) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(redactAttr(a))
		return true
	})
	return h.next.Handle(ctx, out) //nolint:wrapcheck // as requestHandler
}

func (h redact) WithAttrs(as []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(as))
	for i, a := range as {
		out[i] = redactAttr(a)
	}
	return redact{h.next.WithAttrs(out)}
}

func (h redact) WithGroup(name string) slog.Handler { return redact{h.next.WithGroup(name)} }

func redactAttr(a slog.Attr) slog.Attr {
	if secretKey(a.Key) {
		return slog.String(a.Key, Redacted)
	}
	v := a.Value.Resolve()
	switch {
	case v.Kind() == slog.KindGroup:
		group := v.Group()
		out := make([]any, len(group))
		for i, g := range group {
			out[i] = redactAttr(g)
		}
		return slog.Group(a.Key, out...)
	case v.Kind() == slog.KindAny:
		if hdr, ok := v.Any().(http.Header); ok {
			kept := http.Header{}
			for _, name := range loggedHeaders {
				if vals := hdr.Values(name); len(vals) > 0 {
					kept[name] = vals
				}
			}
			return slog.Any(a.Key, kept)
		}
	}
	return slog.Attr{Key: a.Key, Value: v}
}
