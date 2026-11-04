package specref

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"gopkg.in/yaml.v3"
)

// Parity compares a served API with the reference by operation identity
// alone: an operation is the same when its operationId and its route agree.
// Response shapes, parameters and headers are not compared, so decision
// 0003's conventions (the page envelope, the extra headers and responses)
// never count as divergence.

// Untagged groups the operations, reference and served alike, that carry no
// tag.
const Untagged = "untagged"

var (
	ErrNoOperationID = errors.New("specref: operation has no operationId")
	ErrDuplicateOp   = errors.New("specref: operationId is defined twice")
	ErrBaseline      = errors.New("specref: invalid parity baseline")
)

// ParityOperation is one operation of a spec, identified by its operationId
// and its route.
type ParityOperation struct {
	OperationID string
	Method      string // upper case, as a route prints it
	Path        string
	Tag         string // the first tag, or Untagged
	// Designed marks a served operation with x-upstream: none, designed by
	// infraweave where no upstream client reaches. It is never matched with
	// a reference operation, so it is infraweave-only even on a reference
	// route.
	Designed bool
}

// Route is "METHOD /path", the form the report and the baseline print.
func (o ParityOperation) Route() string { return o.Method + " " + o.Path }

// routeKey ignores path parameter names: renaming {id} to {stackId} changes
// no request a client sends, so it does not move the route.
func (o ParityOperation) routeKey() string {
	return o.Method + " " + pathParam.ReplaceAllString(o.Path, "{}")
}

// MovedOperation is a reference operation that the served API holds under
// another route or another operationId: the same operationId at a different
// route, or the reference's route under an operationId the reference does
// not have.
type MovedOperation struct {
	Reference ParityOperation
	Served    ParityOperation
}

// TagParity is the comparison for one tag. Reference operations are filed
// under the reference's tag and infraweave-only operations under the served
// one, so a tag the reference lacks holds only Only.
type TagParity struct {
	Tag     string
	Present []ParityOperation
	Moved   []MovedOperation
	Missing []ParityOperation
	Only    []ParityOperation
}

// Reference counts the tag's reference operations.
func (t TagParity) Reference() int { return len(t.Present) + len(t.Moved) + len(t.Missing) }

// ParityReport is the whole comparison, one TagParity per tag in name order.
type ParityReport struct {
	Tags []TagParity
}

// ParityCounts totals a report.
type ParityCounts struct {
	Reference, Present, Moved, Missing, Only int
}

// Counts totals the report over every tag.
func (r *ParityReport) Counts() ParityCounts {
	var c ParityCounts
	for _, t := range r.Tags {
		c.Reference += t.Reference()
		c.Present += len(t.Present)
		c.Moved += len(t.Moved)
		c.Missing += len(t.Missing)
		c.Only += len(t.Only)
	}
	return c
}

// ParityOperations lists every operation of an OpenAPI document, sorted by
// operationId. Every operation needs a unique operationId, since that is
// what parity matches on.
func ParityOperations(spec []byte) ([]ParityOperation, error) {
	var doc struct {
		Paths map[string]ymap `yaml:"paths"`
	}
	if err := yaml.Unmarshal(spec, &doc); err != nil {
		return nil, fmt.Errorf("specref: parse spec: %w", err)
	}
	var out []ParityOperation
	seen := map[string]string{}
	for _, p := range sortedKeys(doc.Paths) {
		for _, m := range methods {
			op, ok := doc.Paths[p][m].(ymap)
			if !ok {
				continue
			}
			o := ParityOperation{Method: strings.ToUpper(m), Path: p, Tag: Untagged}
			o.OperationID, _ = op["operationId"].(string)
			if o.OperationID == "" {
				return nil, fmt.Errorf("%w: %s", ErrNoOperationID, o.Route())
			}
			if prev, dup := seen[o.OperationID]; dup {
				return nil, fmt.Errorf(
					"%w: %s at %s and %s",
					ErrDuplicateOp,
					o.OperationID,
					prev,
					o.Route(),
				)
			}
			seen[o.OperationID] = o.Route()
			if ts := tags(op); len(ts) > 0 {
				o.Tag = ts[0]
			}
			o.Designed = op["x-upstream"] == "none"
			out = append(out, o)
		}
	}
	slices.SortFunc(
		out,
		func(a, b ParityOperation) int { return strings.Compare(a.OperationID, b.OperationID) },
	)
	return out, nil
}

// CompareParity matches every reference operation with the served
// document's. A reference operation is present when the served document has
// its operationId at its route, and moved when the served document has its
// operationId at another route, or its route under an operationId the
// reference does not use. A served operation no reference operation matches
// is infraweave-only, as is one marked x-upstream: none.
func CompareParity(reference, served []byte) (*ParityReport, error) {
	return compareParity(reference, served, nil)
}

// compareParity is CompareParity over the served operations whose tag owns
// accepts, or all of them when owns is nil.
func compareParity(reference, served []byte, owns func(tag string) bool) (*ParityReport, error) {
	refOps, err := ParityOperations(reference)
	if err != nil {
		return nil, fmt.Errorf("reference: %w", err)
	}
	servedOps, err := ParityOperations(served)
	if err != nil {
		return nil, fmt.Errorf("served: %w", err)
	}
	if owns != nil {
		servedOps = slices.DeleteFunc(servedOps, func(o ParityOperation) bool { return !owns(o.Tag) })
	}
	refIDs := map[string]bool{}
	for _, o := range refOps {
		refIDs[o.OperationID] = true
	}
	byID := map[string]ParityOperation{}
	byRoute := map[string]ParityOperation{}
	for _, o := range servedOps {
		if o.Designed {
			continue
		}
		byID[o.OperationID] = o
		// A served operation that keeps a reference operationId is matched
		// by it, wherever it is routed; only a renamed one is matched by
		// route.
		if !refIDs[o.OperationID] {
			byRoute[o.routeKey()] = o
		}
	}
	groups := map[string]*TagParity{}
	group := func(tag string) *TagParity {
		if groups[tag] == nil {
			groups[tag] = &TagParity{Tag: tag}
		}
		return groups[tag]
	}
	matched := map[string]bool{}
	for _, r := range refOps {
		g := group(r.Tag)
		s, ok := byID[r.OperationID]
		if !ok {
			s, ok = byRoute[r.routeKey()]
		}
		switch {
		case !ok:
			g.Missing = append(g.Missing, r)
		case s.OperationID == r.OperationID && s.routeKey() == r.routeKey():
			g.Present = append(g.Present, r)
		default:
			g.Moved = append(g.Moved, MovedOperation{Reference: r, Served: s})
		}
		if ok {
			matched[s.OperationID] = true
		}
	}
	for _, s := range servedOps {
		if !matched[s.OperationID] {
			g := group(s.Tag)
			g.Only = append(g.Only, s)
		}
	}
	rep := &ParityReport{}
	for _, tag := range sortedKeys(groups) {
		rep.Tags = append(rep.Tags, *groups[tag])
	}
	return rep, nil
}

// Text renders the report: the totals, a table of counts by tag, then every
// operation of each tag with its outcome.
func (r *ParityReport) Text() string {
	var b bytes.Buffer
	c := r.Counts()
	fmt.Fprintf(
		&b,
		"Parity with the reference: %d reference operations, %d present, %d moved, %d missing; %d infraweave-only.\n\n",
		c.Reference,
		c.Present,
		c.Moved,
		c.Missing,
		c.Only,
	)
	width := len("Total")
	for _, t := range r.Tags {
		width = max(width, len(t.Tag))
	}
	// Tags read left-aligned and counts right-aligned under their headings.
	row := func(tag string, n ...int) {
		fmt.Fprintf(
			&b,
			"%-*s  %9d  %7d  %5d  %7d  %15d\n",
			width,
			tag,
			n[0],
			n[1],
			n[2],
			n[3],
			n[4],
		)
	}
	fmt.Fprintf(&b, "%-*s  Reference  Present  Moved  Missing  Infraweave-only\n", width, "Tag")
	for _, t := range r.Tags {
		row(t.Tag, t.Reference(), len(t.Present), len(t.Moved), len(t.Missing), len(t.Only))
	}
	row("Total", c.Reference, c.Present, c.Moved, c.Missing, c.Only)
	for _, t := range r.Tags {
		fmt.Fprintf(&b, "\n%s\n", t.Tag)
		ow := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		for _, o := range t.Present {
			fmt.Fprintf(ow, "  present\t%s\t%s\n", o.OperationID, o.Route())
		}
		for _, m := range t.Moved {
			fmt.Fprintf(
				ow,
				"  moved\t%s\t%s, served as %s %s\n",
				m.Reference.OperationID,
				m.Reference.Route(),
				m.Served.OperationID,
				m.Served.Route(),
			)
		}
		for _, o := range t.Missing {
			fmt.Fprintf(ow, "  missing\t%s\t%s\n", o.OperationID, o.Route())
		}
		for _, o := range t.Only {
			fmt.Fprintf(ow, "  infraweave-only\t%s\t%s\n", o.OperationID, o.Route())
		}
		_ = ow.Flush()
	}
	return b.String()
}

// ParityBaseline is a product's api/parity-baseline.<product>.json: the
// reference operations accepted as not served unchanged. Missing holds
// operationIds by reference tag; Moved holds each deliberate divergence with
// the reason for it. Reasons optionally says why a missing operation is
// accepted, by operationId; a rewrite keeps a reason while its operation
// stays missing.
type ParityBaseline struct {
	Missing map[string][]string `json:"missing"`
	Moved   []BaselineMove      `json:"moved"`
	Reasons map[string]string   `json:"reasons,omitempty"`
}

// BaselineMove records one moved operation: the reference's operationId and
// route, the served ones, and why the served API diverges.
type BaselineMove struct {
	OperationID       string `json:"operationId"`
	Route             string `json:"route"`
	ServedOperationID string `json:"servedOperationId"`
	ServedRoute       string `json:"servedRoute"`
	Reason            string `json:"reason"`
}

func (m BaselineMove) matches(mv MovedOperation) bool {
	return m.OperationID == mv.Reference.OperationID && m.Route == mv.Reference.Route() &&
		m.ServedOperationID == mv.Served.OperationID && m.ServedRoute == mv.Served.Route()
}

// ReadParityBaseline decodes a baseline. It rejects an unknown member, which
// would otherwise be dropped unread, and an operationId listed twice, which
// would leave it unclear which entry the check applies.
func ReadParityBaseline(data []byte) (*ParityBaseline, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var b ParityBaseline
	if err := dec.Decode(&b); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBaseline, err)
	}
	seen := map[string]bool{}
	note := func(id string) error {
		if seen[id] {
			return fmt.Errorf("%w: %s is listed twice", ErrBaseline, id)
		}
		seen[id] = true
		return nil
	}
	for _, tag := range sortedKeys(b.Missing) {
		for _, id := range b.Missing[tag] {
			if err := note(id); err != nil {
				return nil, err
			}
		}
	}
	for _, m := range b.Moved {
		if err := note(m.OperationID); err != nil {
			return nil, err
		}
	}
	return &b, nil
}

// Baseline is the baseline that accepts the report as it stands. A moved
// operation keeps the reason prev records for the same move; a new move, or
// one whose route changed, gets an empty reason for its author to write,
// which the check refuses until they do.
func (r *ParityReport) Baseline(prev *ParityBaseline) *ParityBaseline {
	b := &ParityBaseline{Missing: map[string][]string{}, Moved: []BaselineMove{}}
	for _, t := range r.Tags {
		for _, o := range t.Missing {
			b.Missing[t.Tag] = append(b.Missing[t.Tag], o.OperationID)
			if reason := prev.reason(o.OperationID); reason != "" {
				if b.Reasons == nil {
					b.Reasons = map[string]string{}
				}
				b.Reasons[o.OperationID] = reason
			}
		}
		for _, mv := range t.Moved {
			m := BaselineMove{
				OperationID:       mv.Reference.OperationID,
				Route:             mv.Reference.Route(),
				ServedOperationID: mv.Served.OperationID,
				ServedRoute:       mv.Served.Route(),
			}
			if prev != nil {
				for _, p := range prev.Moved {
					if p.matches(mv) {
						m.Reason = p.Reason
					}
				}
			}
			b.Moved = append(b.Moved, m)
		}
	}
	return b
}

func (b *ParityBaseline) reason(id string) string {
	if b == nil {
		return ""
	}
	return b.Reasons[id]
}

// JSON encodes the baseline as the committed file holds it.
func (b *ParityBaseline) JSON() []byte {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetIndent("", "  ")
	// A reason is prose, so & and < stay as written rather than escaped.
	enc.SetEscapeHTML(false)
	// Strings, slices and a string-keyed map always encode.
	_ = enc.Encode(b)
	return out.Bytes()
}

// ParityDiff is how a report differs from a baseline. Regressions are
// reference operations the baseline does not accept as they are: missing
// without an entry, or moved without a matching entry and a reason.
// Improvements are entries the report no longer needs: the operation is
// served, filed under another tag, or gone from the reference. Both fail the
// check: a regression until the spec or the baseline records it, an
// improvement until make parity-baseline shrinks the baseline.
type ParityDiff struct {
	Regressions  []string
	Improvements []string
}

// Clean reports whether the baseline accepts the report exactly.
func (d ParityDiff) Clean() bool { return len(d.Regressions) == 0 && len(d.Improvements) == 0 }

// outcome is what the report decides for one reference operation.
type outcome struct {
	op    ParityOperation
	state string
	moved MovedOperation
}

// Diff compares the report with the baseline b.
func (r *ParityReport) Diff(b *ParityBaseline) ParityDiff {
	var d ParityDiff
	outcomes := map[string]outcome{}
	for _, t := range r.Tags {
		for _, o := range t.Present {
			outcomes[o.OperationID] = outcome{op: o, state: "present"}
		}
		for _, mv := range t.Moved {
			outcomes[mv.Reference.OperationID] = outcome{
				op:    mv.Reference,
				state: "moved",
				moved: mv,
			}
		}
		for _, o := range t.Missing {
			outcomes[o.OperationID] = outcome{op: o, state: "missing"}
		}
	}
	missingTag := map[string]string{}
	for tag, ids := range b.Missing {
		for _, id := range ids {
			missingTag[id] = tag
		}
	}
	moved := map[string]BaselineMove{}
	for _, m := range b.Moved {
		moved[m.OperationID] = m
	}
	for _, t := range r.Tags {
		for _, o := range t.Missing {
			if _, ok := missingTag[o.OperationID]; !ok {
				d.Regressions = append(d.Regressions, fmt.Sprintf(
					"%s: %s (%s) is missing, and the baseline does not list it",
					t.Tag, o.OperationID, o.Route()))
			}
		}
		for _, mv := range t.Moved {
			d.Regressions = append(d.Regressions, movedRegression(t.Tag, mv, moved)...)
		}
	}
	for tag, ids := range b.Missing {
		for _, id := range ids {
			out, ok := outcomes[id]
			switch {
			case !ok:
				d.Improvements = append(d.Improvements, fmt.Sprintf(
					"%s: %s is listed as missing, and the reference no longer has it", tag, id))
			case out.state != "missing":
				d.Improvements = append(d.Improvements, fmt.Sprintf(
					"%s: %s is listed as missing, and is %s now", tag, id, out.state))
			case out.op.Tag != tag:
				d.Improvements = append(d.Improvements, fmt.Sprintf(
					"%s: %s is listed as missing here, and the reference files it under %s",
					tag,
					id,
					out.op.Tag,
				))
			}
		}
	}
	for _, id := range sortedKeys(b.Reasons) {
		if _, ok := missingTag[id]; !ok {
			d.Improvements = append(d.Improvements, fmt.Sprintf(
				"%s has a reason, and the baseline does not list it as missing", id))
		}
	}
	for _, m := range b.Moved {
		out, ok := outcomes[m.OperationID]
		switch {
		case !ok:
			d.Improvements = append(d.Improvements, fmt.Sprintf(
				"%s is listed as moved, and the reference no longer has it", m.OperationID))
		case out.state != "moved":
			d.Improvements = append(d.Improvements, fmt.Sprintf(
				"%s: %s is listed as moved, and is %s now", out.op.Tag, m.OperationID, out.state))
		}
	}
	slices.Sort(d.Regressions)
	slices.Sort(d.Improvements)
	return d
}

func movedRegression(tag string, mv MovedOperation, moved map[string]BaselineMove) []string {
	at := fmt.Sprintf(
		"%s: %s (%s) is served as %s (%s)",
		tag,
		mv.Reference.OperationID,
		mv.Reference.Route(),
		mv.Served.OperationID,
		mv.Served.Route(),
	)
	m, ok := moved[mv.Reference.OperationID]
	switch {
	case !ok:
		return []string{at + ", and the baseline does not record the move"}
	case !m.matches(mv):
		return []string{fmt.Sprintf("%s, and the baseline records it as %s (%s) served as %s (%s)",
			at, m.OperationID, m.Route, m.ServedOperationID, m.ServedRoute)}
	case strings.TrimSpace(m.Reason) == "":
		return []string{at + ", and the baseline gives no reason for the move"}
	}
	return nil
}

// Text renders the diff for a reader who must act on it.
func (d ParityDiff) Text() string {
	if d.Clean() {
		return "The baseline accepts the report exactly.\n"
	}
	var b strings.Builder
	for _, s := range d.Regressions {
		fmt.Fprintf(&b, "regression: %s\n", s)
	}
	for _, s := range d.Improvements {
		fmt.Fprintf(&b, "improvement (apply with make parity-baseline): %s\n", s)
	}
	return b.String()
}

// ParityFiles names the inputs of a parity run. Product scopes the served
// operations to its own tags; the zero Product compares them all.
type ParityFiles struct {
	Reference, Served, Baseline string
	Product                     Product
}

// ParityResult is a parity run: the report, the baseline it was checked
// against (empty when the file does not exist yet) and the diff.
type ParityResult struct {
	Report   *ParityReport
	Baseline *ParityBaseline
	Diff     ParityDiff
}

// ParityCheck compares the files f name.
func ParityCheck(f ParityFiles) (*ParityResult, error) {
	ref, err := os.ReadFile(f.Reference)
	if err != nil {
		return nil, fmt.Errorf("specref: %w", err)
	}
	served, err := os.ReadFile(f.Served)
	if err != nil {
		return nil, fmt.Errorf("specref: %w", err)
	}
	rep, err := f.Product.Parity(ref, served)
	if err != nil {
		return nil, err
	}
	b := &ParityBaseline{}
	data, err := os.ReadFile(f.Baseline)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("specref: %w", err)
	default:
		if b, err = ReadParityBaseline(data); err != nil {
			return nil, err
		}
	}
	return &ParityResult{Report: rep, Baseline: b, Diff: rep.Diff(b)}, nil
}

// WriteParityBaseline rewrites the baseline file to accept the current
// report, keeping the reasons it records for unchanged moves, and returns
// the baseline it wrote.
func WriteParityBaseline(f ParityFiles) (*ParityBaseline, error) {
	res, err := ParityCheck(f)
	if err != nil {
		return nil, err
	}
	b := res.Report.Baseline(res.Baseline)
	files := map[string][]byte{filepath.Base(f.Baseline): b.JSON()}
	if err := writeFiles(filepath.Dir(f.Baseline), files); err != nil {
		return nil, fmt.Errorf("specref: %w", err)
	}
	return b, nil
}
