package iactest

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/weaveplatform/infraweave/internal/iac"
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

// Goroutines is how many goroutines call a vendor at once to show that
// ValidateSettings shares no state between calls.
const Goroutines = 8

// reasons are the field-error reasons a client may branch on.
var reasons = []string{
	kernel.ReasonRequired, kernel.ReasonType, kernel.ReasonEnum, kernel.ReasonFormat,
	kernel.ReasonPattern, kernel.ReasonMinLength, kernel.ReasonMaxLength, kernel.ReasonMinimum,
	kernel.ReasonMaximum, kernel.ReasonUnknownField, kernel.ReasonDuplicate,
	kernel.ReasonNotFound, kernel.ReasonRule,
}

// Run fails t with each of Problems.
func Run(t *testing.T, v iac.Vendor, cases []Case) {
	t.Helper()
	for _, p := range Problems(v, cases) {
		t.Error(p)
	}
}

// Problems holds v to the contract and to cases, the vendor's own:
//   - v is registered under its Kind;
//   - each case refuses exactly the fields it names, every refusal with a
//     reason clients branch on, a message, and a pointer to a field the
//     settings hold, or the empty pointer for the settings as a whole;
//   - ValidateSettings leaves its input as it found it and answers each
//     case the same from Goroutines goroutines at once as it did alone.
func Problems(v iac.Vendor, cases []Case) []string {
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
	slices.Sort(out)
	return slices.Compact(out)
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
