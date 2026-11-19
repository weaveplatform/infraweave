package problem

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"reflect"
	"runtime"
	"strings"
)

const (
	diagnosticEnv       = "INFRAWEAVE_DIAGNOSTIC_HTTP500"
	diagnosticMarker    = "http500-v1"
	diagnosticTypeLimit = 8
	diagnosticSymbolMax = 256
)

// diagnoseFailure is temporary CI-only metadata collection. Error text and
// values can contain credentials or SQL, so only concrete types and a validated
// SQLSTATE leave the error chain. It must not replace the original response if
// an error's metadata method or the logger panics.
func diagnoseFailure(w http.ResponseWriter, err error) {
	if os.Getenv(diagnosticEnv) != "1" {
		return
	}
	defer func() { _ = recover() }()
	meta := diagnosticErrors(err)
	callers := diagnosticCallers()
	stage := "unknown"
	if len(callers) > 0 {
		stage = callers[0]
	}
	slog.Error("unexpected HTTP failure diagnostic",
		"diagnostic", diagnosticMarker,
		"request_id", w.Header().Get(RequestIDHeader),
		"stage", stage,
		"callers", callers,
		"error_types", meta.types,
		"sqlstate", meta.state,
		"canceled", meta.canceled,
		"deadline", meta.deadline,
		"truncated", meta.truncated,
	)
}

type diagnosticMetadata struct {
	types     []string
	state     string
	canceled  bool
	deadline  bool
	truncated bool
}

// diagnosticErrors bounds both the returned metadata and the inspected tree,
// including joined errors. Cancellation flags name markers found within that
// bound, with truncation explicit. It never formats an error or reads its fields.
//
//nolint:errorlint // Recursive errors.As/Is can skip siblings or loop; this walk inspects only the current node.
func diagnosticErrors(err error) diagnosticMetadata {
	var meta diagnosticMetadata
	pending := []error{err}
	for visited := 0; len(pending) > 0 && visited < diagnosticTypeLimit; visited++ {
		current := pending[0]
		pending = pending[1:]
		if current == nil {
			continue
		}
		meta.types = append(meta.types, diagnosticErrorType(current))
		switch current {
		case context.Canceled:
			meta.canceled = true
		case context.DeadlineExceeded:
			meta.deadline = true
		}
		if code, ok := current.(interface{ SQLState() string }); ok && meta.state == "" {
			if value := code.SQLState(); diagnosticSQLState(value) {
				meta.state = value
			}
		}
		var children []error
		switch next := current.(type) {
		case interface{ Unwrap() error }:
			children = []error{next.Unwrap()}
		case interface{ Unwrap() []error }:
			children = next.Unwrap()
		}
		remaining := max(0, diagnosticTypeLimit-visited-1-len(pending))
		n := min(len(children), remaining)
		pending = append(pending, children[:n]...)
		if n < len(children) {
			meta.truncated = true
		}
	}
	return meta
}

func diagnosticSQLState(value string) bool {
	if len(value) != 5 {
		return false
	}
	for i := range len(value) {
		if c := value[i]; (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// diagnosticCallers starts outside Failure, retaining the actual middleware or
// ResponseError caller rather than arguments, PCs, file paths or stack values.
func diagnosticCallers() []string {
	var pcs [3]uintptr
	n := runtime.Callers(4, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	var out []string
	for {
		frame, more := frames.Next()
		if frame.Function != "" {
			out = append(out, diagnosticSymbol(frame.Function))
		}
		if !more || len(out) == len(pcs) {
			return out
		}
	}
}

func diagnosticSymbol(value string) string {
	return value[:min(len(value), diagnosticSymbolMax)]
}

// Unnamed types and generic arguments can include struct tags. Only named
// identity and pointer/kind markers are diagnostic metadata.
func diagnosticErrorType(err error) string {
	typ := reflect.TypeOf(err)
	prefix := ""
	for range 4 {
		if typ.Name() != "" || typ.Kind() != reflect.Pointer {
			break
		}
		prefix += "*"
		typ = typ.Elem()
	}
	if name := typ.Name(); name != "" {
		if i := strings.IndexByte(name, '['); i >= 0 {
			name = name[:i]
		}
		if pkg := typ.PkgPath(); pkg != "" {
			name = pkg + "." + name
		}
		return diagnosticSymbol(prefix + name)
	}
	return prefix + "<" + typ.Kind().String() + ">"
}
