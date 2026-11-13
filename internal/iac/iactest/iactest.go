package iactest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac"
	"github.com/weaveplatform/infraweave/internal/iac/plan"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Case is one settings document a vendor is given, with the pointers of
// the fields it must refuse; none when it accepts the settings.
type Case struct {
	Name     string
	Settings string
	Refused  []string
}

// contract are the cases every vendor answers alike: settings with no
// fields, which leave every setting at its default, and settings that are
// not an object, refused as a whole.
var contract = []Case{
	{Name: "no settings", Settings: `{}`},
	{Name: "an array", Settings: `[]`, Refused: []string{""}},
	{Name: "a string", Settings: `"x"`, Refused: []string{""}},
	{Name: "not JSON", Settings: `{`, Refused: []string{""}},
}

// PlanCase is one plan document a vendor parses, with the plan it must
// answer, or the error it must refuse it with when Err is set.
type PlanCase struct {
	Name     string
	Document []byte
	Want     plan.Plan
	Err      error
}

// planContract are the plans every vendor answers alike: an empty plan of
// format 1.x has no changes and a zero delta, and a document that is not
// JSON, of another major format, or past plan.MaxSize is refused.
var planContract = []PlanCase{
	{
		Name: "an empty plan", Document: []byte(`{"format_version":"1.2"}`),
		Want: plan.Plan{Changes: []plan.Change{}},
	},
	{Name: "a plan that is not JSON", Document: []byte(`{`), Err: plan.ErrMalformed},
	{
		Name: "a plan of another major format", Document: []byte(`{"format_version":"2.0"}`),
		Err: plan.ErrFormat,
	},
}

// Recorded are the plan cases of a vendor's recorded plans in dir, one of
// internal/iac/plan/testdata's directories, each with the plan its
// scenario must parse to: all.json one change of every type the tools
// plan, none.json none, and outputs.json an output's change alone. The
// state before each held the seven resources of test/planfixtures' first
// configuration.
func Recorded(dir string) ([]PlanCase, error) {
	all := planned(
		change("data.terraform_remote_state.read", plan.TypeRead),
		change("terraform_data.add", plan.TypeAdd),
		change("terraform_data.change", plan.TypeChange),
		change("terraform_data.delete", plan.TypeDelete),
		change("terraform_data.forget", plan.TypeForget),
		change("terraform_data.imported", plan.TypeImport),
		change("terraform_data.keep", plan.TypeNoop),
		moved("terraform_data.new", "terraform_data.old"),
		change("terraform_data.replace", plan.TypeReplaceDestroyBeforeCreate),
		change("terraform_data.replace_first", plan.TypeReplaceCreateBeforeDestroy),
	)
	all.Delta = plan.Delta{
		Add: 3, Change: 1, Delete: 3, Import: 1, Move: 1, Forget: 1, Resources: 7, Outputs: true,
	}
	none := planned(
		change("terraform_data.change", plan.TypeNoop),
		change("terraform_data.delete", plan.TypeNoop),
		change("terraform_data.forget", plan.TypeNoop),
		change("terraform_data.keep", plan.TypeNoop),
		change("terraform_data.old", plan.TypeNoop),
		change("terraform_data.replace", plan.TypeNoop),
		change("terraform_data.replace_first", plan.TypeNoop),
	)
	none.Delta = plan.Delta{Resources: 7}
	outputs := planned(none.Changes...)
	outputs.Delta = plan.Delta{Resources: 7, Outputs: true}
	var cases []PlanCase
	for name, want := range map[string]plan.Plan{"all": all, "none": none, "outputs": outputs} {
		doc, err := os.ReadFile(filepath.Join(dir, name+".json"))
		if err != nil {
			return nil, fmt.Errorf("recorded plan %s: %w", name, err)
		}
		cases = append(cases, PlanCase{Name: "recorded " + name, Document: doc, Want: want})
	}
	slices.SortFunc(cases, func(a, b PlanCase) int { return strings.Compare(a.Name, b.Name) })
	return cases, nil
}

// planned is a plan of changes and a zero delta.
func planned(changes ...plan.Change) plan.Plan {
	return plan.Plan{Changes: slices.Clone(changes)}
}

// change is a planned change of address's resource of type t.
func change(address string, t plan.Type) plan.Change {
	return plan.Change{
		Address: address, Metadata: plan.Metadata{Result: plan.ResultPlanned, Type: t},
	}
}

// moved is a planned change that moves from's resource to address and
// changes nothing else.
func moved(address, from string) plan.Change {
	c := change(address, plan.TypeNoop)
	c.PreviousAddress, c.Metadata.Moved = &from, true
	return c
}

// oversized names the contract's plan past plan.MaxSize, which is read
// from a generated reader rather than held as a case.
const oversized = "a plan past MaxSize"

// Goroutines is how many goroutines call a vendor at once to show that
// ValidateSettings and ParsePlan share no state between calls.
const Goroutines = 8

// reasons are the field-error reasons a client may branch on.
var reasons = []string{
	kernel.ReasonRequired, kernel.ReasonType, kernel.ReasonEnum, kernel.ReasonFormat,
	kernel.ReasonPattern, kernel.ReasonMinLength, kernel.ReasonMaxLength, kernel.ReasonMinimum,
	kernel.ReasonMaximum, kernel.ReasonUnknownField, kernel.ReasonDuplicate,
	kernel.ReasonNotFound, kernel.ReasonRule,
}

// Run fails t with each of Problems.
func Run(t *testing.T, v iac.Vendor, cases []Case, plans ...PlanCase) {
	t.Helper()
	for _, p := range Problems(v, cases, plans...) {
		t.Error(p)
	}
}

// Problems holds v to the contract and to cases and plans, the vendor's
// own:
//   - v is registered under its Kind;
//   - each case refuses exactly the fields it names, every refusal with a
//     reason clients branch on, a message, and a pointer to a field the
//     settings hold, or the empty pointer for the settings as a whole;
//   - ValidateSettings leaves its input as it found it and answers each
//     case the same from Goroutines goroutines at once as it did alone;
//   - ParsePlan answers each plan case its plan, or refuses it with its
//     error, refuses a plan past plan.MaxSize, and answers the same from
//     Goroutines goroutines at once.
func Problems(v iac.Vendor, cases []Case, plans ...PlanCase) []string {
	out := settingsProblems(v, cases)
	out = append(out, planProblems(v, slices.Concat(planContract, plans))...)
	if _, err := v.ParsePlan(io.MultiReader(
		strings.NewReader(`{"format_version":"1.2","pad":"`),
		io.LimitReader(padding{}, plan.MaxSize),
	)); !errors.Is(err, plan.ErrTooLarge) {
		out = append(out, fmt.Sprintf("%s: ParsePlan = %v, want %v", oversized, err,
			plan.ErrTooLarge))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// padding reads as an endless run of the byte a. It fills p by doubling
// copies rather than byte by byte, since the race detector instruments
// each byte a loop writes and every Problems call reads 64 MiB of it.
type padding struct{}

func (padding) Read(p []byte) (int, error) {
	n := copy(p, "a")
	for n < len(p) {
		n += copy(p[n:], p[:n])
	}
	return len(p), nil
}

// planProblems holds v's ParsePlan to plans, and its answers from
// Goroutines goroutines at once to its answers alone.
func planProblems(v iac.Vendor, plans []PlanCase) []string {
	var out []string
	alone := make([]answer, len(plans))
	for i, c := range plans {
		got, err := v.ParsePlan(bytes.NewReader(c.Document))
		alone[i] = answer{plan: got, err: fmt.Sprint(err)}
		switch {
		case c.Err != nil && !errors.Is(err, c.Err):
			out = append(out, fmt.Sprintf("%s: ParsePlan = %v, want %v", c.Name, err, c.Err))
		case c.Err == nil && (err != nil || !reflect.DeepEqual(got, c.Want)):
			out = append(out, fmt.Sprintf("%s: ParsePlan = %+v, %v, want %+v", c.Name, got, err,
				c.Want))
		}
	}
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for range Goroutines {
		wg.Go(func() {
			for i, c := range plans {
				got, err := v.ParsePlan(bytes.NewReader(c.Document))
				if !reflect.DeepEqual(answer{plan: got, err: fmt.Sprint(err)}, alone[i]) {
					mu.Lock()
					out = append(
						out,
						c.Name+": a concurrent call answered otherwise than a call alone",
					)
					mu.Unlock()
				}
			}
		})
	}
	wg.Wait()
	return out
}

// answer is what one ParsePlan call answered, its error by its text, since
// each call wraps its own.
type answer struct {
	plan plan.Plan
	err  string
}

// settingsProblems holds v's registration and ValidateSettings to the
// contract's cases and cases.
func settingsProblems(v iac.Vendor, cases []Case) []string {
	var out []string
	if r, ok := iac.Lookup(v.Kind()); !ok || r.Kind() != v.Kind() {
		out = append(out, fmt.Sprintf("kind %q is not registered", v.Kind()))
	}
	all := slices.Concat(contract, cases)
	alone := make([][]kernel.FieldError, len(all))
	for i, c := range all {
		in := []byte(c.Settings)
		alone[i] = v.ValidateSettings(in)
		if string(in) != c.Settings {
			out = append(out, c.Name+": ValidateSettings changed its input")
		}
		out = append(out, caseProblems(c, alone[i])...)
	}
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for range Goroutines {
		wg.Go(func() {
			for i, c := range all {
				if got := v.ValidateSettings(
					[]byte(c.Settings),
				); !reflect.DeepEqual(
					got,
					alone[i],
				) {
					mu.Lock()
					out = append(
						out,
						c.Name+": a concurrent call answered otherwise than a call alone",
					)
					mu.Unlock()
				}
			}
		})
	}
	wg.Wait()
	return out
}

func caseProblems(c Case, got []kernel.FieldError) []string {
	var out []string
	pointers := make([]string, 0, len(got))
	for _, e := range got {
		pointers = append(pointers, e.Pointer)
		switch {
		case !slices.Contains(reasons, e.Reason):
			out = append(out, fmt.Sprintf("%s: %s's reason %q is not one clients branch on",
				c.Name, e.Pointer, e.Reason))
		case e.Message == "":
			out = append(out, fmt.Sprintf("%s: %s has no message", c.Name, e.Pointer))
		case !resolves(c.Settings, e.Pointer):
			out = append(
				out,
				fmt.Sprintf("%s: %s names no field of the settings", c.Name, e.Pointer),
			)
		}
	}
	slices.Sort(pointers)
	want := slices.Sorted(slices.Values(c.Refused))
	if !slices.Equal(pointers, want) {
		out = append(out, fmt.Sprintf("%s: refused %q, want %q", c.Name, pointers, want))
	}
	return out
}

// resolves reports whether pointer, a JSON Pointer, names a value of
// settings; the empty pointer names the settings themselves.
func resolves(settings, pointer string) bool {
	if pointer == "" {
		return true
	}
	if !strings.HasPrefix(pointer, "/") {
		return false
	}
	var at any
	if json.Unmarshal([]byte(settings), &at) != nil {
		return false
	}
	unescape := strings.NewReplacer("~1", "/", "~0", "~")
	for _, token := range strings.Split(pointer[1:], "/") {
		token = unescape.Replace(token)
		switch node := at.(type) {
		case map[string]any:
			next, ok := node[token]
			if !ok {
				return false
			}
			at = next
		case []any:
			i, err := strconv.Atoi(token)
			if err != nil || i < 0 || i >= len(node) {
				return false
			}
			at = node[i]
		default:
			return false
		}
	}
	return true
}
