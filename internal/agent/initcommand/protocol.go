package initcommand

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// The bounds of one evaluation's commands and of each call's frames.
//
// ref: docs/implementation/worker-pool-contracts.md § "Worker initialization and signed runs"
const (
	// MaxCalls is the number of exec calls one evaluation may make.
	MaxCalls = 8
	// CallTimeout bounds each call, within the evaluation's own deadline.
	CallTimeout = 60 * time.Second
	// MaxOutput bounds each of a call's standard output and error.
	MaxOutput = 1 << 20
	// MaxCombined bounds the output of all of an evaluation's calls.
	MaxCombined = 4 << 20
	// MaxRequest bounds the request frame on the helper's standard input.
	MaxRequest = 64 << 10
	// MaxArgs, MaxArg and MaxArgv bound a call's argument vector: its
	// length, each argument and their sum.
	MaxArgs = 128
	MaxArg  = 4 << 10
	MaxArgv = 32 << 10
	// MaxChunk bounds each output frame the helper writes.
	MaxChunk = 64 << 10
	// maxResult bounds the result frame.
	maxResult = 4 << 10
)

// Version is the request frame's version.
const Version = 1

// Request is the private frame a helper reads on its standard input: the
// call's number within its evaluation, the argument vector it executes
// without a shell, and how long it may run.
type Request struct {
	Version       int      `json:"version"`
	Call          int      `json:"call"`
	Argv          []string `json:"argv"`
	TimeoutMillis int64    `json:"timeoutMillis"`
}

// Statuses of a helper's result.
const (
	// StatusExited is a command that ended by itself; ExitCode is its
	// exit code, 127 when it could not start and -1 when a signal ended it.
	StatusExited = "exited"
	// StatusTimeout is a command the helper ended at its deadline.
	StatusTimeout = "timeout"
	// StatusOutput is a command the helper ended for exceeding MaxOutput.
	StatusOutput = "output-limit"
	// StatusRefused is a request the helper refused before starting it.
	StatusRefused = "refused"
)

// Result is the frame that ends a helper's output.
type Result struct {
	Call     int    `json:"call"`
	Status   string `json:"status"`
	ExitCode int    `json:"exitCode"`
	Reason   string `json:"reason,omitempty"`
}

// The kinds of frame a helper writes on its standard output: chunks of
// the command's output, then one result.
const (
	kindStdout byte = 1
	kindStderr byte = 2
	kindResult byte = 3
)

// ErrFrame marks a request or output stream that breaks its framing or
// bounds.
var ErrFrame = errors.New("invalid initialization command frame")

// checkArgv refuses an argument vector beyond the call's bounds.
func checkArgv(argv []string) error {
	if len(argv) == 0 || len(argv) > MaxArgs || argv[0] == "" {
		return fmt.Errorf("%w: %d arguments", ErrFrame, len(argv))
	}
	total := 0
	for _, arg := range argv {
		if len(arg) > MaxArg || strings.ContainsRune(arg, 0) {
			return fmt.Errorf("%w: an argument exceeds %d bytes or holds NUL", ErrFrame, MaxArg)
		}
		total += len(arg)
	}
	if total > MaxArgv {
		return fmt.Errorf("%w: the arguments exceed %d bytes", ErrFrame, MaxArgv)
	}
	return nil
}

// Validate refuses a request of another version, call number, deadline
// or argument vector than the bounds allow.
func (r Request) Validate() error {
	if r.Version != Version || r.Call < 1 || r.Call > MaxCalls ||
		r.TimeoutMillis < 1 || r.TimeoutMillis > CallTimeout.Milliseconds() {
		return fmt.Errorf("%w: version %d, call %d, timeout %dms", ErrFrame, r.Version, r.Call, r.TimeoutMillis)
	}
	return checkArgv(r.Argv)
}

// EncodeRequest is r's frame: its JSON's length as a big-endian uint32,
// then the JSON.
func EncodeRequest(r Request) ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("encode the initialization command: %w", err)
	}
	if len(body) > MaxRequest {
		return nil, fmt.Errorf("%w: the request exceeds %d bytes", ErrFrame, MaxRequest)
	}
	frame := binary.BigEndian.AppendUint32(make([]byte, 0, 4+len(body)), uint32(len(body))) //nolint:gosec // bounded above
	return append(frame, body...), nil
}

// ReadRequest reads one request frame from r, which must then end. The
// JSON is strict: an unknown field, trailing data or a bound it breaks
// refuses it.
func ReadRequest(r io.Reader) (Request, error) {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return Request{}, fmt.Errorf("%w: the request's length: %w", ErrFrame, err)
	}
	n := binary.BigEndian.Uint32(size[:])
	if n == 0 || n > MaxRequest {
		return Request{}, fmt.Errorf("%w: a request of %d bytes", ErrFrame, n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return Request{}, fmt.Errorf("%w: the request is truncated: %w", ErrFrame, err)
	}
	if extra, _ := r.Read(make([]byte, 1)); extra != 0 {
		return Request{}, fmt.Errorf("%w: trailing bytes after the request", ErrFrame)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var req Request
	if err := dec.Decode(&req); err != nil {
		return Request{}, fmt.Errorf("%w: the request: %w", ErrFrame, err)
	}
	if dec.More() {
		return Request{}, fmt.Errorf("%w: the request has trailing data", ErrFrame)
	}
	return req, req.Validate()
}

// writeFrame writes one output frame: its kind, its length as a
// big-endian uint32, and p.
func writeFrame(w io.Writer, kind byte, p []byte) error {
	header := binary.BigEndian.AppendUint32([]byte{kind}, uint32(len(p))) //nolint:gosec // callers bound p
	if _, err := w.Write(append(header, p...)); err != nil {
		return fmt.Errorf("write an initialization command frame: %w", err)
	}
	return nil
}

// WriteOutput writes p as frames of the stream kind, stdout when stderr
// is false, of at most MaxChunk bytes each.
func WriteOutput(w io.Writer, stderr bool, p []byte) error {
	kind := kindStdout
	if stderr {
		kind = kindStderr
	}
	for len(p) > 0 {
		n := min(len(p), MaxChunk)
		if err := writeFrame(w, kind, p[:n]); err != nil {
			return err
		}
		p = p[n:]
	}
	return nil
}

// WriteResult writes the result frame that ends a helper's output.
func WriteResult(w io.Writer, r Result) error {
	if len(r.Reason) > 256 {
		r.Reason = r.Reason[:256]
	}
	body, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode the initialization command's result: %w", err)
	}
	return writeFrame(w, kindResult, body)
}

// output is what one call's helper wrote: the command's standard output
// and error, and its result.
type output struct {
	stdout, stderr bytes.Buffer
	result         *Result
}

func (o *output) size() int64 { return int64(o.stdout.Len() + o.stderr.Len()) }

// errOverflow marks output beyond its per-stream or combined bound.
var errOverflow = errors.New("initialization command output exceeds its bounds")

// readOutput reads a helper's frames from r until it ends, bounding each
// stream by MaxOutput and both by budget before it keeps any chunk. The
// result frame, of call, must come last.
func readOutput(r io.Reader, call int, budget int64) (*output, error) {
	o := &output{}
	var header [5]byte
	for {
		if _, err := io.ReadFull(r, header[:]); err != nil {
			if errors.Is(err, io.EOF) && o.result != nil {
				return o, nil
			}
			return o, fmt.Errorf("%w: the output ended without its result: %w", ErrFrame, err)
		}
		if o.result != nil {
			return o, fmt.Errorf("%w: output after the result", ErrFrame)
		}
		kind, n := header[0], int64(binary.BigEndian.Uint32(header[1:]))
		var dst *bytes.Buffer
		switch kind {
		case kindStdout:
			dst = &o.stdout
		case kindStderr:
			dst = &o.stderr
		case kindResult:
			res, err := readResult(r, n, call)
			if err != nil {
				return o, err
			}
			o.result = &res
			continue
		default:
			return o, fmt.Errorf("%w: frame kind %d", ErrFrame, kind)
		}
		if n == 0 || n > MaxChunk {
			return o, fmt.Errorf("%w: a chunk of %d bytes", ErrFrame, n)
		}
		if int64(dst.Len())+n > MaxOutput || o.size()+n > budget {
			return o, errOverflow
		}
		if _, err := io.CopyN(dst, r, n); err != nil {
			return o, fmt.Errorf("%w: a truncated chunk: %w", ErrFrame, err)
		}
	}
}

func readResult(r io.Reader, n int64, call int) (Result, error) {
	if n == 0 || n > maxResult {
		return Result{}, fmt.Errorf("%w: a result of %d bytes", ErrFrame, n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return Result{}, fmt.Errorf("%w: a truncated result: %w", ErrFrame, err)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var res Result
	if err := dec.Decode(&res); err != nil || dec.More() {
		return Result{}, fmt.Errorf("%w: the result: %w", ErrFrame, err)
	}
	if res.Call != call {
		return Result{}, fmt.Errorf("%w: the result of call %d, not %d", ErrFrame, res.Call, call)
	}
	return res, nil
}
