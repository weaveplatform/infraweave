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

// Parity decides one of four outcomes for every reference operation:
// served, served with divergence, superseded or missing. A reference
// operation is matched with the served one that has its operationId, or the
// name the rename table gives it. The match is then compared by method,
// route and its query, header and cookie parameters; path parameters are
// compared as part of the route, and response shapes and headers are not
// compared. The API conventions (pagination, idempotency keys, rate limits)
// are recorded by the scaffolder, so the parameters they add are never a
// difference, and an operation that differs only by them is served.
//
// ref: decisions 0001, 0003 and 0016

// Untagged groups the operations, reference and served alike, that carry no
// tag.
const Untagged = "untagged"

// OrganizationTag is the served tag of infraweave's own organization
// operations, which no Spacelift product has. Like x-upstream: none, it puts
// an operation outside the parity surface.
const OrganizationTag = "Organization"

// The differences parity detects between a reference operation and its
// served match, as x-infraweave-divergence records them. A parameter
// difference is DivergenceParameter followed by the parameter's name.
const (
	DivergenceOperationID = "operationId"
	DivergenceMethod      = "method"
	DivergencePath        = "path"
	DivergenceParameter   = "parameter:"
)

// conventionParams are the parameters each API convention adds, keyed as
// ParityOperation.Params keys them.
//
// ref: decision 0003
var conventionParams = map[string][]string{
	ConventionPagination:  {"query cursor", "query limit"},
	ConventionIdempotency: {"header Idempotency-Key"},
}

var (
	ErrNoOperationID = errors.New("specref: operation has no operationId")
	ErrDuplicateOp   = errors.New("specref: operationId is defined twice")
	ErrBaseline      = errors.New("specref: invalid parity baseline")
	ErrRegister      = errors.New("specref: invalid register")
)

// Outcome is the parity verdict on one reference operation.
//
// ref: decision 0016
type Outcome string

const (
	OutcomeServed     Outcome = "served"
	OutcomeDivergent  Outcome = "served with divergence"
	OutcomeSuperseded Outcome = "superseded"
	OutcomeMissing    Outcome = "missing"
)

// Outcomes lists every outcome in the order the report prints them.
func Outcomes() []Outcome {
	return []Outcome{OutcomeServed, OutcomeDivergent, OutcomeSuperseded, OutcomeMissing}
}

// ParityOperation is one operation of a spec, identified by its operationId
// and its route.
type ParityOperation struct {
	OperationID string
	Method      string // upper case, as a route prints it
	Path        string
	Tag         string // the first tag, or Untagged
	// Designed marks a served operation outside the parity surface: one with
	// x-upstream: none, designed by infraweave where no upstream client
	// reaches, or one under OrganizationTag. It is never matched with a
	// reference operation, so it is infraweave-only even on a reference
	// route.
	Designed bool
	// Params holds the query, header and cookie parameters, keyed by "in
	// name", each with whether it is required.
	Params map[string]bool
	// Divergence is the operation's x-infraweave-divergence.
	Divergence []string
	// RootField is the GraphQL root field a Deploy reference operation
	// projects, which ties it to the Terraform provider's calls.
	RootField string
}

// Route is "METHOD /path", the form the report and the baseline print.
func (o ParityOperation) Route() string { return o.Method + " " + o.Path }

// pathKey ignores path parameter names: renaming {id} to {stackId} changes
// no request a client sends, so it does not move the route.
func pathKey(p string) string { return pathParam.ReplaceAllString(p, "{}") }

// OperationParity is the outcome for one reference operation. Served is its
// served match, absent when the outcome is missing, or superseded and not
// served. Differences are those parity detects; Supersession is the
// supersessions register record that leaves the operation out.
type OperationParity struct {
	Reference    ParityOperation
	Outcome      Outcome
	Served       *ParityOperation
	Differences  []string
	Supersession string
}

// TagParity is the comparison for one tag. Reference operations are filed
// under the reference's tag and infraweave-only operations under the served
// one, so a tag the reference lacks holds only Only.
type TagParity struct {
	Tag        string
	Operations []OperationParity
	Only       []ParityOperation
}

// Count counts the tag's reference operations with outcome o.
func (t TagParity) Count(o Outcome) int {
	n := 0
	for _, op := range t.Operations {
		if op.Outcome == o {
			n++
		}
	}
	return n
}

// ParityReport is the whole comparison, one TagParity per tag in name order.
// Failures are the breaches of the parity rules, such as an unrecorded or
// stale divergence or a served superseded operation, sorted: each fails the
// check whatever the baseline accepts.
//
// ref: decision 0016
type ParityReport struct {
	Tags     []TagParity
	Failures []string
}

// ParityCounts totals a report.
type ParityCounts struct {
	Reference, Only int
	By              map[Outcome]int
}

// Counts totals the report over every tag.
func (r *ParityReport) Counts() ParityCounts {
	c := ParityCounts{By: map[Outcome]int{}}
	for _, t := range r.Tags {
		c.Reference += len(t.Operations)
		c.Only += len(t.Only)
		for _, o := range Outcomes() {
			c.By[o] += t.Count(o)
		}
	}
	return c
}

// Operation is the outcome for the reference operation id, or false when the
// reference has none.
func (r *ParityReport) Operation(id string) (OperationParity, bool) {
	for _, t := range r.Tags {
		for _, op := range t.Operations {
			if op.Reference.OperationID == id {
				return op, true
			}
		}
	}
	return OperationParity{}, false
}

// ParityRegisters are the registers parity reads beside the two documents.
// The zero value renames and supersedes nothing.
type ParityRegisters struct {
	// Renames maps a reference identifier to the one infraweave serves:
	// the rename table's identifier rows that name no schema and no
	// wildcard.
	//
	// ref: decision 0001
	Renames map[string]string
	// Superseded maps each of the product's reference operationIds that a
	// superseded record lists to that record's id.
	Superseded map[string]string
}

// ReadParityRegisters reads docs/data/renames.json and
// docs/data/supersessions.json for product: only the supersessions' entries
// prefixed "<product>:" are the product's.
func ReadParityRegisters(renames, supersessions []byte, product string) (*ParityRegisters, error) {
	var rn struct {
		Records []struct {
			Kind       string `json:"kind"`
			Schema     string `json:"schema"`
			Reference  string `json:"reference"`
			Infraweave string `json:"infraweave"`
		} `json:"records"`
	}
	if err := json.Unmarshal(renames, &rn); err != nil {
		return nil, fmt.Errorf("%w: renames: %w", ErrRegister, err)
	}
	var sup struct {
		Records []struct {
			ID         string   `json:"id"`
			Treatment  string   `json:"treatment"`
			Operations []string `json:"operations"`
		} `json:"records"`
	}
	if err := json.Unmarshal(supersessions, &sup); err != nil {
		return nil, fmt.Errorf("%w: supersessions: %w", ErrRegister, err)
	}
	reg := &ParityRegisters{Renames: map[string]string{}, Superseded: map[string]string{}}
	for _, r := range rn.Records {
		if r.Kind == "identifier" && r.Schema == "" && !strings.Contains(r.Reference, "*") {
			reg.Renames[r.Reference] = r.Infraweave
		}
	}
	for _, r := range sup.Records {
		if r.Treatment != "superseded" {
			continue
		}
		for _, op := range r.Operations {
			if id, ok := strings.CutPrefix(op, product+":"); ok {
				reg.Superseded[id] = r.ID
			}
		}
	}
	return reg, nil
}

// renameID is the operationId infraweave serves for a reference one.
func (reg *ParityRegisters) renameID(id string) string {
	if to, ok := reg.Renames[id]; ok {
		return to
	}
	return id
}

// renamePath applies every path rename to p. A rename names whole segments,
// so /spacelift renames /spacelift/x but not /spacelift-intelligence; the
// longest rename applies first.
func (reg *ParityRegisters) renamePath(p string) string {
	var from []string
	for k := range reg.Renames {
		if strings.HasPrefix(k, "/") {
			from = append(from, k)
		}
	}
	slices.SortFunc(from, func(a, b string) int { return len(b) - len(a) })
	for _, k := range from {
		var out strings.Builder
		rest := p
		for {
			i := strings.Index(rest, k)
			if i < 0 {
				out.WriteString(rest)
				break
			}
			end := i + len(k)
			if end == len(rest) || rest[end] == '/' {
				out.WriteString(rest[:i] + reg.Renames[k])
			} else {
				out.WriteString(rest[:end])
			}
			rest = rest[end:]
		}
		p = out.String()
	}
	return p
}

// ParityOperations lists every operation of an OpenAPI document, sorted by
// operationId. Every operation needs a unique operationId, since that is
// what parity matches on.
func ParityOperations(spec []byte) ([]ParityOperation, error) {
	var doc struct {
		Paths      map[string]ymap `yaml:"paths"`
		Components struct {
			Parameters map[string]ymap `yaml:"parameters"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(spec, &doc); err != nil {
		return nil, fmt.Errorf("specref: parse spec: %w", err)
	}
	var out []ParityOperation
	seen := map[string]string{}
	for _, p := range sortedKeys(doc.Paths) {
		item := doc.Paths[p]
		for _, m := range methods {
			op, ok := item[m].(ymap)
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
			o.Designed = op["x-upstream"] == "none" || o.Tag == OrganizationTag
			o.Params = params(doc.Components.Parameters, item["parameters"], op["parameters"])
			o.Divergence = tags(ymap{"tags": op["x-infraweave-divergence"]})
			if g, ok := op["x-graphql"].(ymap); ok &&
				(g["type"] == "Query" || g["type"] == "Mutation") {
				o.RootField, _ = g["field"].(string)
			}
			out = append(out, o)
		}
	}
	slices.SortFunc(
		out,
		func(a, b ParityOperation) int { return strings.Compare(a.OperationID, b.OperationID) },
	)
	return out, nil
}

// params collects the non-path parameters of a path item and an operation,
// resolving references to the document's parameter components. An
// operation's parameter replaces the path item's of the same name and
// location, as OpenAPI says.
func params(components map[string]ymap, lists ...any) map[string]bool {
	out := map[string]bool{}
	for _, list := range lists {
		items, _ := list.([]any)
		for _, it := range items {
			p, _ := it.(ymap)
			if ref, ok := p["$ref"].(string); ok {
				p = components[strings.TrimPrefix(ref, componentsPrefix+"parameters/")]
			}
			in, _ := p["in"].(string)
			name, _ := p["name"].(string)
			if in == "" || in == "path" || name == "" {
				continue
			}
			required, _ := p["required"].(bool)
			out[in+" "+name] = required
		}
	}
	return out
}

// CompareParity decides the outcome of every reference operation against
// the whole served document, with no renames and no supersessions.
func CompareParity(reference, served []byte) (*ParityReport, error) {
	return compareParity(reference, served, nil, nil)
}

// compareParity is CompareParity over the served operations whose tag owns
// accepts, or all of them when owns is nil, with the registers reg.
func compareParity(
	reference, served []byte,
	owns func(tag string) bool,
	reg *ParityRegisters,
) (*ParityReport, error) {
	if reg == nil {
		reg = &ParityRegisters{}
	}
	refOps, err := ParityOperations(reference)
	if err != nil {
		return nil, fmt.Errorf("reference: %w", err)
	}
	servedOps, err := ParityOperations(served)
	if err != nil {
		return nil, fmt.Errorf("served: %w", err)
	}
	if owns != nil {
		servedOps = slices.DeleteFunc(
			servedOps,
			func(o ParityOperation) bool { return !owns(o.Tag) },
		)
	}
	byID := map[string]ParityOperation{}
	for _, o := range servedOps {
		if !o.Designed {
			byID[o.OperationID] = o
		}
	}
	groups := map[string]*TagParity{}
	group := func(tag string) *TagParity {
		if groups[tag] == nil {
			groups[tag] = &TagParity{Tag: tag}
		}
		return groups[tag]
	}
	rep := &ParityReport{}
	matched := map[string]bool{}
	for _, r := range refOps {
		s, ok := byID[reg.renameID(r.OperationID)]
		if !ok {
			s, ok = byID[r.OperationID]
		}
		op := OperationParity{Reference: r, Outcome: OutcomeMissing}
		if ok {
			matched[s.OperationID] = true
			op.Served = &s
			var unrecorded, stale []string
			op.Differences, unrecorded, stale = differences(r, s, reg)
			op.Outcome = OutcomeServed
			if len(op.Differences) > 0 || slices.ContainsFunc(s.Divergence, notConvention) {
				op.Outcome = OutcomeDivergent
			}
			at := fmt.Sprintf("%s: %s (%s) is served as %s (%s)",
				r.Tag, r.OperationID, r.Route(), s.OperationID, s.Route())
			if len(unrecorded) > 0 {
				rep.Failures = append(rep.Failures, fmt.Sprintf(
					"%s, and differs in %s without recording it in x-infraweave-divergence",
					at, strings.Join(unrecorded, ", ")))
			}
			if len(stale) > 0 {
				rep.Failures = append(rep.Failures, fmt.Sprintf(
					"%s, and its x-infraweave-divergence records %s, which it does not differ in",
					at, strings.Join(stale, ", ")))
			}
		}
		if sup, ok := reg.Superseded[r.OperationID]; ok {
			op.Supersession = sup
			if op.Served != nil {
				rep.Failures = append(rep.Failures, fmt.Sprintf(
					"%s: %s (%s) is superseded by %s, and is served as %s (%s)",
					r.Tag, r.OperationID, r.Route(), sup,
					op.Served.OperationID, op.Served.Route()))
			}
			op.Outcome = OutcomeSuperseded
		}
		g := group(r.Tag)
		g.Operations = append(g.Operations, op)
	}
	for _, s := range servedOps {
		if !matched[s.OperationID] {
			g := group(s.Tag)
			g.Only = append(g.Only, s)
		}
	}
	for _, tag := range sortedKeys(groups) {
		rep.Tags = append(rep.Tags, *groups[tag])
	}
	slices.Sort(rep.Failures)
	return rep, nil
}

func notConvention(name string) bool {
	switch name {
	case ConventionPagination, ConventionIdempotency, ConventionRateLimit:
		return false
	}
	return true
}

// detectable reports whether parity detects the difference name, so that a
// record of it that no difference needs is stale. Other recorded names, such
// as a response-shape divergence, are beyond what parity compares.
func detectable(name string) bool {
	return name == DivergenceMethod || name == DivergencePath ||
		strings.HasPrefix(name, DivergenceParameter)
}

// differences compares a reference operation with its served match. It
// returns every difference, the ones neither the rename table nor
// x-infraweave-divergence records, and the recorded detectable ones that do
// not occur.
func differences(r, s ParityOperation, reg *ParityRegisters) (all, unrecorded, stale []string) {
	recorded := map[string]bool{}
	for _, d := range s.Divergence {
		recorded[d] = true
	}
	renamed := map[string]bool{}
	if s.OperationID != r.OperationID {
		all = append(all, DivergenceOperationID)
		renamed[DivergenceOperationID] = true
	}
	if s.Method != r.Method {
		all = append(all, DivergenceMethod)
	}
	if pathKey(s.Path) != pathKey(r.Path) {
		all = append(all, DivergencePath)
		renamed[DivergencePath] = pathKey(s.Path) == pathKey(reg.renamePath(r.Path))
	}
	excused := map[string]bool{}
	for _, d := range s.Divergence {
		for _, p := range conventionParams[d] {
			excused[p] = true
		}
	}
	keys := map[string]bool{}
	for k := range r.Params {
		keys[k] = true
	}
	for k := range s.Params {
		keys[k] = true
	}
	for _, k := range sortedKeys(keys) {
		req, inRef := r.Params[k]
		sreq, inServed := s.Params[k]
		if !inRef && excused[k] {
			continue
		}
		if inRef != inServed || req != sreq {
			_, name, _ := strings.Cut(k, " ")
			all = append(all, DivergenceParameter+name)
		}
	}
	for _, d := range all {
		if !renamed[d] && !recorded[d] {
			unrecorded = append(unrecorded, d)
		}
	}
	for _, d := range s.Divergence {
		if detectable(d) && !slices.Contains(all, d) {
			stale = append(stale, d)
		}
	}
	return all, unrecorded, stale
}

// Text renders the totals and a table of counts by tag.
func (r *ParityReport) Text() string {
	var b bytes.Buffer
	c := r.Counts()
	fmt.Fprintf(
		&b,
		"Parity with the reference: %d reference operations: %d served, %d served with divergence, %d superseded, %d missing; %d infraweave-only.\n\n",
		c.Reference,
		c.By[OutcomeServed],
		c.By[OutcomeDivergent],
		c.By[OutcomeSuperseded],
		c.By[OutcomeMissing],
		c.Only,
	)
	width := len("Total")
	for _, t := range r.Tags {
		width = max(width, len(t.Tag))
	}
	// Tags read left-aligned and counts right-aligned under their headings.
	row := func(tag string, n ...int) {
		fmt.Fprintf(&b, "%-*s  %9d  %6d  %9d  %10d  %7d  %15d\n",
			width, tag, n[0], n[1], n[2], n[3], n[4], n[5])
	}
	fmt.Fprintf(&b, "%-*s  Reference  Served  Divergent  Superseded  Missing  Infraweave-only\n",
		width, "Tag")
	for _, t := range r.Tags {
		row(t.Tag, len(t.Operations), t.Count(OutcomeServed), t.Count(OutcomeDivergent),
			t.Count(OutcomeSuperseded), t.Count(OutcomeMissing), len(t.Only))
	}
	row("Total", c.Reference, c.By[OutcomeServed], c.By[OutcomeDivergent],
		c.By[OutcomeSuperseded], c.By[OutcomeMissing], c.Only)
	if len(r.Failures) > 0 {
		fmt.Fprintf(&b, "\nFailures (%d):\n", len(r.Failures))
		for _, f := range r.Failures {
			fmt.Fprintf(&b, "failure: %s\n", f)
		}
	}
	return b.String()
}

// Listing renders every operation of each tag with its outcome.
func (r *ParityReport) Listing() string {
	var b bytes.Buffer
	for _, t := range r.Tags {
		fmt.Fprintf(&b, "\n%s\n", t.Tag)
		ow := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		for _, o := range Outcomes() {
			for _, op := range t.Operations {
				if op.Outcome != o {
					continue
				}
				detail := op.Reference.Route()
				switch {
				case o == OutcomeSuperseded:
					detail += " (" + op.Supersession + ")"
				case op.Served != nil && o == OutcomeDivergent:
					detail += fmt.Sprintf(", served as %s %s: %s", op.Served.OperationID,
						op.Served.Route(), strings.Join(divergences(op), ", "))
				}
				fmt.Fprintf(ow, "  %s\t%s\t%s\n", o, op.Reference.OperationID, detail)
			}
		}
		for _, o := range t.Only {
			fmt.Fprintf(ow, "  infraweave-only\t%s\t%s\n", o.OperationID, o.Route())
		}
		_ = ow.Flush()
	}
	return b.String()
}

// divergences lists what makes a served operation divergent: the detected
// differences and every recorded divergence that is not a convention.
func divergences(op OperationParity) []string {
	out := slices.Clone(op.Differences)
	for _, d := range op.Served.Divergence {
		if notConvention(d) && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out
}

// ParityBaseline is a product's api/parity-baseline.<product>.json: the
// computed missing list, operationIds by reference tag. Reasons optionally
// says why a missing operation is accepted, by operationId; a rewrite keeps
// a reason while its operation stays missing.
type ParityBaseline struct {
	Missing map[string][]string `json:"missing"`
	Reasons map[string]string   `json:"reasons,omitempty"`
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
	for _, tag := range sortedKeys(b.Missing) {
		for _, id := range b.Missing[tag] {
			if seen[id] {
				return nil, fmt.Errorf("%w: %s is listed twice", ErrBaseline, id)
			}
			seen[id] = true
		}
	}
	return &b, nil
}

// Baseline is the baseline that accepts the report as it stands, keeping
// the reasons prev gives for operations still missing.
func (r *ParityReport) Baseline(prev *ParityBaseline) *ParityBaseline {
	b := &ParityBaseline{Missing: map[string][]string{}}
	for _, t := range r.Tags {
		for _, op := range t.Operations {
			if op.Outcome != OutcomeMissing {
				continue
			}
			id := op.Reference.OperationID
			b.Missing[t.Tag] = append(b.Missing[t.Tag], id)
			if reason := prev.reason(id); reason != "" {
				if b.Reasons == nil {
					b.Reasons = map[string]string{}
				}
				b.Reasons[id] = reason
			}
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
// missing reference operations the baseline does not list. Improvements are
// entries the report no longer needs: the operation is served, superseded,
// filed under another tag, or gone from the reference. Both fail the check:
// a regression until the spec serves the operation or the baseline accepts
// it, an improvement until make parity-baseline shrinks the baseline.
type ParityDiff struct {
	Regressions  []string
	Improvements []string
}

// Clean reports whether the baseline accepts the report exactly.
func (d ParityDiff) Clean() bool { return len(d.Regressions) == 0 && len(d.Improvements) == 0 }

// Diff compares the report with the baseline b.
func (r *ParityReport) Diff(b *ParityBaseline) ParityDiff {
	var d ParityDiff
	missingTag := map[string]string{}
	for tag, ids := range b.Missing {
		for _, id := range ids {
			missingTag[id] = tag
		}
	}
	for _, t := range r.Tags {
		for _, op := range t.Operations {
			if _, ok := missingTag[op.Reference.OperationID]; op.Outcome == OutcomeMissing && !ok {
				d.Regressions = append(d.Regressions, fmt.Sprintf(
					"%s: %s (%s) is missing, and the baseline does not list it",
					t.Tag, op.Reference.OperationID, op.Reference.Route()))
			}
		}
	}
	for tag, ids := range b.Missing {
		for _, id := range ids {
			op, ok := r.Operation(id)
			switch {
			case !ok:
				d.Improvements = append(d.Improvements, fmt.Sprintf(
					"%s: %s is listed as missing, and the reference no longer has it", tag, id))
			case op.Outcome != OutcomeMissing:
				d.Improvements = append(d.Improvements, fmt.Sprintf(
					"%s: %s is listed as missing, and is %s now", tag, id, op.Outcome))
			case op.Reference.Tag != tag:
				d.Improvements = append(d.Improvements, fmt.Sprintf(
					"%s: %s is listed as missing here, and the reference files it under %s",
					tag, id, op.Reference.Tag))
			}
		}
	}
	for _, id := range sortedKeys(b.Reasons) {
		if _, ok := missingTag[id]; !ok {
			d.Improvements = append(d.Improvements, fmt.Sprintf(
				"%s has a reason, and the baseline does not list it as missing", id))
		}
	}
	slices.Sort(d.Regressions)
	slices.Sort(d.Improvements)
	return d
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
// operations to its own tags; the zero Product compares them all. Renames
// and Supersessions are the registers; either may be empty for none.
type ParityFiles struct {
	Reference, Served, Baseline string
	Renames, Supersessions      string
	Product                     Product
}

// ParityResult is a parity run: the report, the baseline it was checked
// against (empty when the file does not exist yet) and the diff.
type ParityResult struct {
	Report   *ParityReport
	Baseline *ParityBaseline
	Diff     ParityDiff
}

// Passed reports whether the run breaks no rule and the baseline accepts it
// exactly.
func (r *ParityResult) Passed() bool { return len(r.Report.Failures) == 0 && r.Diff.Clean() }

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
	reg, err := readRegisters(f)
	if err != nil {
		return nil, err
	}
	rep, err := f.Product.Parity(ref, served, reg)
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

func readRegisters(f ParityFiles) (*ParityRegisters, error) {
	read := func(p string) ([]byte, error) {
		if p == "" {
			return []byte("{}"), nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("specref: %w", err)
		}
		return data, nil
	}
	rn, err := read(f.Renames)
	if err != nil {
		return nil, err
	}
	sup, err := read(f.Supersessions)
	if err != nil {
		return nil, err
	}
	return ReadParityRegisters(rn, sup, f.Product.Name)
}

// WriteParityBaseline rewrites the baseline file to accept the current
// report, keeping the reasons it records for operations still missing, and
// returns the baseline it wrote.
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
