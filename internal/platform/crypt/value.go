package crypt

import (
	"fmt"
	"io"
	"log/slog"
)

// redacted is how a Value renders everywhere but Reveal.
const redacted = "[redacted]"

// Value is an opened secret. Every way fmt, log/slog, encoding/json and
// encoding/text render it prints [redacted], so a value that reaches a
// log line, an error or a response by mistake leaks nothing. Code that
// needs the bytes calls Reveal, and review looks at every call.
type Value struct{ b []byte }

// Reveal returns the secret's bytes.
func (v Value) Reveal() []byte { return v.b }

func (Value) String() string   { return redacted }
func (Value) GoString() string { return redacted }

// Format renders every verb, %x and %#v among them, as [redacted].
func (Value) Format(f fmt.State, _ rune) { _, _ = io.WriteString(f, redacted) }

func (Value) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }
func (Value) MarshalText() ([]byte, error) { return []byte(redacted), nil }
func (Value) LogValue() slog.Value         { return slog.StringValue(redacted) }
