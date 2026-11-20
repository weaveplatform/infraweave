package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/weaveplatform/infraweave/pkg/sdk"
)

// ExampleCapture is the synthetic capture that shows the layout. It is
// validated and replayed like any other, but records nothing Spacelift did,
// so it never counts as a capture.
const ExampleCapture = "C0"

// The files of one capture directory.
//
// ref: decision 0016
const (
	MetaFile     = "meta.yaml"
	RequestFile  = "request.graphql"
	ResponseFile = "response.json"
	ExpectFile   = "expect.yaml"
)

// CaptureMeta is a capture's meta.yaml: where and when it was taken, and
// what was redacted before it was committed.
type CaptureMeta struct {
	// Entry is the capture's open question in docs/data/open-questions.json,
	// and the name of its directory.
	//
	// ref: report 30
	Entry string `yaml:"entry"`
	// Example marks ExampleCapture.
	Example bool `yaml:"example"`
	// Captured is the date of the exchange, YYYY-MM-DD.
	Captured string `yaml:"captured"`
	// Tier is the Spacelift plan of the account it was taken on.
	Tier string `yaml:"tier"`
	// Introspection is sha256:<hex> of the account's introspection result,
	// which tells a reader whether the committed snapshot saw the same schema.
	Introspection string `yaml:"introspection"`
	// CapturedBy names who took it.
	CapturedBy string `yaml:"capturedBy"`
	// Redacted lists what was replaced before the files were committed.
	Redacted []string `yaml:"redacted"`
}

// CaptureCall is one infraweave REST call that stands for the captured
// exchange, and what infraweave must answer.
type CaptureCall struct {
	Method string `yaml:"method"`
	// Path starts at the API listener's root, /api/v1 included.
	Path    string            `yaml:"path"`
	Headers map[string]string `yaml:"headers"`
	// Body is sent as JSON when present.
	Body   any           `yaml:"body"`
	Expect CaptureExpect `yaml:"expect"`
}

// CaptureExpect holds the assertions on one answer.
type CaptureExpect struct {
	Status int `yaml:"status"`
	// Problem is the slug of the problem type the answer must carry.
	Problem string `yaml:"problem"`
	// Body maps a JSON Pointer into the answer to the value it must hold.
	Body map[string]any `yaml:"body"`
}

// CaptureHandoff assigns a recorded assertion to its executable subset or to
// the owner of the remaining implementation or evidence. It never changes the
// historical value in Observed.
type CaptureHandoff struct {
	Classification string `yaml:"classification"`
	Owner          string `yaml:"owner"`
	Detail         string `yaml:"detail"`
}

// Capture is one checked-in capture directory.
type Capture struct {
	ID    string
	Meta  CaptureMeta
	Calls []CaptureCall
	// ReplayAfter names the implementation issue that enables REST replay.
	// Observed still verifies the recorded response before that issue lands.
	ReplayAfter string
	// ReplayWith names a stateful REST replayer for invariants such as request
	// time bounds that cannot be expressed as static JSON pointer equality.
	ReplayWith string
	// ReplaySubset executes only assertions classified rest in Handoffs. Its
	// success cannot count as completion of the whole capture's REST contract.
	ReplaySubset string
	Handoffs     map[string]CaptureHandoff
	Observed     map[string]any
}

var (
	captureID     = regexp.MustCompile(`^C(0|[1-9][0-9]*)$`)
	introspection = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

	// ErrCapture is returned for a capture directory that breaks the layout.
	ErrCapture = errors.New("capture")
	// ErrParity is returned when infraweave's answer differs from what a
	// capture expects: a parity bug, not a flaky test.
	ErrParity = errors.New("parity")
)

// LoadCaptures reads and validates every capture directory under root.
// entries are the capture records of the open-questions register; a
// capture other than the example must name one of them.
//
// ref: report 30
func LoadCaptures(root string, entries map[string]bool) ([]Capture, error) {
	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("captures: %w", err)
	}
	var out []Capture
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		c, err := loadCapture(filepath.Join(root, d.Name()), entries)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func loadCapture(dir string, entries map[string]bool) (Capture, error) {
	id := filepath.Base(dir)
	fail := func(format string, args ...any) (Capture, error) {
		return Capture{}, fmt.Errorf("%w %s: %s", ErrCapture, id, fmt.Sprintf(format, args...))
	}
	if !captureID.MatchString(id) {
		return fail("the directory is not named C<n>")
	}
	c := Capture{ID: id}
	if err := decodeStrict(filepath.Join(dir, MetaFile), &c.Meta); err != nil {
		return fail("%v", err)
	}
	var expect struct {
		Calls        []CaptureCall             `yaml:"calls"`
		ReplayAfter  string                    `yaml:"replayAfter"`
		ReplayWith   string                    `yaml:"replayWith"`
		ReplaySubset string                    `yaml:"replaySubset"`
		Handoffs     map[string]CaptureHandoff `yaml:"handoffs"`
		Observed     map[string]any            `yaml:"observed"`
	}
	if err := decodeStrict(filepath.Join(dir, ExpectFile), &expect); err != nil {
		return fail("%v", err)
	}
	c.Calls = expect.Calls
	c.ReplayAfter = expect.ReplayAfter
	c.ReplayWith = expect.ReplayWith
	c.ReplaySubset = expect.ReplaySubset
	c.Handoffs = expect.Handoffs
	c.Observed = expect.Observed
	if reason := c.invalid(entries); reason != "" {
		return fail("%s", reason)
	}
	if _, err := os.Stat(filepath.Join(dir, RequestFile)); err != nil {
		return fail("%v", err)
	}
	resp, err := os.ReadFile(filepath.Join(dir, ResponseFile))
	if err != nil {
		return fail("%v", err)
	}
	if !json.Valid(resp) {
		return fail("%s is not JSON", ResponseFile)
	}
	var doc any
	_ = json.Unmarshal(resp, &doc)
	for _, ptr := range slices.Sorted(mapKeys(c.Observed)) {
		value, ok := pointer(doc, ptr)
		if !strings.HasPrefix(ptr, "/") || !ok || !sameJSON(value, c.Observed[ptr]) {
			return fail("observed %s = %v, want %v", ptr, value, c.Observed[ptr])
		}
	}
	return c, nil
}

// decodeStrict decodes one YAML document, failing on a key the type lacks.
func decodeStrict(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err //nolint:wrapcheck // the caller names the capture
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return nil
}

// invalid returns why the capture breaks the layout, or "" when it holds.
func (c *Capture) invalid(entries map[string]bool) string {
	m := c.Meta
	switch {
	case m.Entry != c.ID:
		return fmt.Sprintf("meta.yaml entry %q is not the directory's name", m.Entry)
	case m.Example != (c.ID == ExampleCapture):
		return "only " + ExampleCapture + " is the example, and it is marked example: true"
	case !m.Example && !entries[m.Entry]:
		return m.Entry + " is no capture record of docs/data/open-questions.json"
	case m.Tier == "" || m.CapturedBy == "" || len(m.Redacted) == 0:
		return "meta.yaml needs tier, capturedBy and redacted"
	case !introspection.MatchString(m.Introspection):
		return "meta.yaml introspection is not sha256:<64 hex digits>"
	case len(c.Calls) == 0 && (strings.TrimSpace(c.ReplayAfter) == "" && strings.TrimSpace(c.ReplayWith) == "" && strings.TrimSpace(c.ReplaySubset) == "" || len(c.Observed) == 0):
		return "expect.yaml has no calls"
	case c.ReplayAfter != "" && len(c.Calls) != 0:
		return "expect.yaml cannot defer REST replay and supply calls"
	case c.ReplayWith != "" && (len(c.Calls) != 0 || c.ReplayAfter != ""):
		return "expect.yaml cannot mix a stateful replayer with calls or deferral"
	case c.ReplaySubset != "" && (len(c.Calls) != 0 || c.ReplayAfter != "" || c.ReplayWith != ""):
		return "expect.yaml cannot mix a subset replayer with calls, deferral or a full replayer"
	}
	if reason := c.invalidHandoffs(); reason != "" {
		return reason
	}
	if _, err := time.Parse(time.DateOnly, m.Captured); err != nil {
		return "meta.yaml captured: " + err.Error()
	}
	for i, call := range c.Calls {
		if reason := call.invalid(); reason != "" {
			return fmt.Sprintf("expect.yaml call %d: %s", i+1, reason)
		}
	}
	return ""
}

func (c *Capture) invalidHandoffs() string {
	if c.ReplaySubset == "" {
		if len(c.Handoffs) != 0 {
			return "expect.yaml handoffs require a subset replayer"
		}
		return ""
	}
	rest := 0
	for _, ptr := range slices.Sorted(mapKeys(c.Observed)) {
		h, ok := c.Handoffs[ptr]
		if !ok {
			return "expect.yaml has no handoff for " + ptr
		}
		if !slices.Contains([]string{"rest", "deferred", "upstream", "inference", "deviation"}, h.Classification) ||
			strings.TrimSpace(h.Owner) == "" || strings.TrimSpace(h.Detail) == "" {
			return "expect.yaml has invalid handoff for " + ptr
		}
		if h.Classification == "rest" {
			rest++
		}
	}
	if len(c.Handoffs) != len(c.Observed) {
		return "expect.yaml handoffs must name exactly the observed assertions"
	}
	if rest == 0 || rest == len(c.Handoffs) {
		return "expect.yaml subset needs both REST assertions and remaining handoffs"
	}
	return ""
}

// CheckSubset requires a replayer to identify exactly the assertions it executed.
// An empty handler or an unrelated successful operation cannot complete a subset.
func (c *Capture) CheckSubset(executed []string) error {
	fail := func(reason string) error {
		return fmt.Errorf("%w %s: REST subset %s", ErrParity, c.ID, reason)
	}
	if c.ReplaySubset == "" || len(executed) == 0 {
		return fail("executed no assertions")
	}
	want := 0
	for _, h := range c.Handoffs {
		if h.Classification == "rest" {
			want++
		}
	}
	seen := make(map[string]bool, len(executed))
	for _, ptr := range executed {
		if c.Handoffs[ptr].Classification != "rest" || seen[ptr] {
			return fail("returned unknown, non-REST or duplicate assertion " + ptr)
		}
		seen[ptr] = true
	}
	if len(seen) != want {
		return fail("did not execute every declared REST assertion")
	}
	return nil
}

// invalid returns why the call cannot be replayed, or "" when it can.
func (call *CaptureCall) invalid() string {
	methods := []string{
		http.MethodGet,
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
	}
	switch {
	case !slices.Contains(methods, call.Method):
		return fmt.Sprintf("method %q", call.Method)
	case !strings.HasPrefix(call.Path, "/"):
		return fmt.Sprintf("path %q does not start with /", call.Path)
	case http.StatusText(call.Expect.Status) == "":
		return fmt.Sprintf("status %d", call.Expect.Status)
	case call.Expect.Problem != "" && !slices.Contains(sdk.Slugs(), call.Expect.Problem):
		return fmt.Sprintf("problem %q is not in docs/data/problems.json", call.Expect.Problem)
	}
	for ptr := range call.Expect.Body {
		if !strings.HasPrefix(ptr, "/") {
			return fmt.Sprintf("body assertion %q is not a JSON Pointer", ptr)
		}
	}
	return ""
}

// Replay sends call through c, which sends each call once, and checks the
// answer against its expectation.
func Replay(ctx context.Context, c *sdk.Client, call CaptureCall) error {
	headers := make([]sdk.Header, 0, len(call.Headers))
	for _, k := range slices.Sorted(mapKeys(call.Headers)) {
		headers = append(headers, sdk.Header{Name: k, Value: call.Headers[k]})
	}
	resp, problemErr := c.Do(ctx, call.Method, call.Path, call.Body, headers...)
	if resp == nil {
		return fmt.Errorf("replay: %w", problemErr)
	}
	what := call.Method + " " + call.Path
	want := call.Expect
	if resp.Status != want.Status {
		return fmt.Errorf(
			"%w: %s answered %d, want %d: %s",
			ErrParity,
			what,
			resp.Status,
			want.Status,
			resp.Body,
		)
	}
	if want.Problem != "" {
		if p, ok := sdk.AsProblem(problemErr); !ok || p.Slug() != want.Problem {
			return fmt.Errorf(
				"%w: %s answered %s, want problem %q",
				ErrParity,
				what,
				resp.Body,
				want.Problem,
			)
		}
	}
	var doc any
	if len(want.Body) > 0 && json.Unmarshal(resp.Body, &doc) != nil {
		return fmt.Errorf("%w: %s answered no JSON: %s", ErrParity, what, resp.Body)
	}
	for _, ptr := range slices.Sorted(mapKeys(want.Body)) {
		v, ok := pointer(doc, ptr)
		if !ok || !sameJSON(v, want.Body[ptr]) {
			return fmt.Errorf(
				"%w: %s answered %s = %v, want %v",
				ErrParity,
				what,
				ptr,
				v,
				want.Body[ptr],
			)
		}
	}
	return nil
}

func mapKeys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// pointer resolves an RFC 6901 JSON Pointer in a decoded JSON document.
func pointer(doc any, ptr string) (any, bool) {
	if ptr == "" {
		return doc, true
	}
	for _, tok := range strings.Split(ptr, "/")[1:] {
		tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
		switch v := doc.(type) {
		case map[string]any:
			next, ok := v[tok]
			if !ok {
				return nil, false
			}
			doc = next
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(v) {
				return nil, false
			}
			doc = v[i]
		default:
			return nil, false
		}
	}
	return doc, true
}

// sameJSON compares a decoded JSON value with a YAML one through their JSON
// encodings, so YAML's 501 equals JSON's 501.0.
func sameJSON(got, want any) bool {
	b, err := json.Marshal(want)
	if err != nil {
		return false
	}
	var w any
	if json.Unmarshal(b, &w) != nil {
		return false
	}
	return reflect.DeepEqual(got, w)
}

// The shapes TestUnitCaptureRedaction refuses: what a capture's redaction
// must replace before it is committed, since no Spacelift credential or
// customer data may enter the repository.
//
// ref: decision 0002
var leaks = []struct {
	what string
	re   *regexp.Regexp
}{
	{"a JWT", regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]*`)},
	{"an AWS access key", regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"a private key block", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{
		"an API key secret",
		regexp.MustCompile(
			`(?i)"?\b(secret|api_?key|apiKeySecret|password|token)"?\s*[:=]\s*"?[A-Za-z0-9+/_=-]{16,}`,
		),
	},
	{"an e-mail address outside example.com", regexp.MustCompile(
		`[A-Za-z0-9._%+-]+@(?:[A-Za-z0-9-]+\.)*(?:[A-Za-z0-9-]+)\.[A-Za-z]{2,}`)},
}

// exampleMail is an address under example.com, which RFC 2606 reserves.
var exampleMail = regexp.MustCompile(`@(?:[A-Za-z0-9-]+\.)*example\.com$`)

// Leaks returns, for every file under root, each match that looks like a
// credential or a real person's e-mail address, as "path:line: what".
func Leaks(root string) ([]string, error) {
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("leaks: %w", err)
	}
	defer dir.Close()
	var out []string
	err = fs.WalkDir(dir.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(dir.FS(), rel)
		if err != nil {
			return err //nolint:wrapcheck // wrapped below
		}
		path := filepath.Join(root, rel)
		for n, line := range strings.Split(string(data), "\n") {
			for _, l := range leaks {
				for _, m := range l.re.FindAllString(line, -1) {
					if l.what == leaks[len(leaks)-1].what && exampleMail.MatchString(m) {
						continue
					}
					out = append(out, fmt.Sprintf("%s:%d: %s", path, n+1, l.what))
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("leaks: %w", err)
	}
	return out, nil
}
