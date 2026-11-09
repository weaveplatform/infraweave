package search

import (
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// Type is a field's type, which decides the constraints that apply to it.
type Type string

// The field types of the reference's SearchSuggestionsFieldType.
const (
	Boolean   Type = "BOOLEAN"
	Enum      Type = "ENUM"
	String    Type = "STRING"
	Hierarchy Type = "HIERARCHY"
	Time      Type = "TIME"
)

// Field is a searchable field of an entity, as its suggestions describe
// it. Every field filters; Orderable ones order. Values are an ENUM's.
type Field struct {
	Name      string
	Type      Type
	Orderable bool
	Values    []string
}

// Input is a search: a page of the rows that every predicate and the full
// text phrase keep, in OrderBy's order.
type Input struct {
	// First is the page size, clamped as kernel.PageLimit clamps a limit.
	First *int32
	// After is the cursor of a previous page's edge.
	After string
	// Page is which page to read; the zero value reads the page after
	// After, or the first when there is no After.
	Page Page
	// FullText keeps the rows whose text fields contain it, ignoring case.
	FullText   string
	Predicates []Predicate
	OrderBy    []Order
}

// SuggestionsInput asks which values Fields hold among the rows the
// predicates and the full text phrase keep. A nil Fields asks for every
// field; a name the entity lacks is ignored, as the reference ignores it.
type SuggestionsInput struct {
	Fields     []string
	FullText   string
	Predicates []Predicate
}

// Page is the reference's SearchQueryRequestedPage.
type Page string

// The pages a search reads. Previous is the page before After.
const (
	First    Page = "FIRST"
	Next     Page = "NEXT"
	Previous Page = "PREVIOUS"
	Last     Page = "LAST"
)

// Predicate keeps the rows whose Field meets Constraint, or, with Exclude,
// the rows whose Field does not. A row's field that is unset meets none.
type Predicate struct {
	Field      string
	Exclude    bool
	Constraint Constraint
}

// Constraint is the reference's SearchQueryFieldConstraint. Exactly one
// member is set; a list's values are alternatives, so an empty list keeps
// no row.
type Constraint struct {
	BooleanEquals []bool
	EnumEquals    []string
	StringEquals  []string
	// StringMatches values match ignoring case, '*' standing for any run
	// of characters.
	StringMatches []string
	// HierarchyValues are nodes, each kept with its descendants.
	HierarchyValues []string
	Hierarchy       []Node
	TimeInRange     *TimeRange
	TimeInLast      Window
}

// Node is a hierarchy node by its ID, alone or with its descendants.
type Node struct {
	Equals      string
	Descendants bool
}

// TimeRange holds the times from Start to End, Unix seconds, inclusive; an
// unset end is open.
type TimeRange struct {
	Start, End *int32
}

// Window is the reference's SearchQueryFieldConstraintTimeInLast.
type Window string

// windows are the reference's time windows.
var windows = map[Window]time.Duration{
	"LAST15M": 15 * time.Minute,
	"LAST30M": 30 * time.Minute,
	"LAST1H":  time.Hour,
	"LAST4H":  4 * time.Hour,
	"LAST1D":  24 * time.Hour,
	"LAST7D":  7 * 24 * time.Hour,
	"LAST14D": 14 * 24 * time.Hour,
}

// Duration is how far back w reaches, and whether w is a window.
func (w Window) Duration() (time.Duration, bool) {
	d, ok := windows[w]
	return d, ok
}

// Order orders by Field, descending with Desc.
type Order struct {
	Field string
	Desc  bool
}

// The field-error reasons that a search answers.
const (
	ReasonRequired     = kernel.ReasonRequired
	ReasonEnum         = kernel.ReasonEnum
	ReasonUnknownField = kernel.ReasonUnknownField
	ReasonDuplicate    = kernel.ReasonDuplicate
	ReasonRule         = kernel.ReasonRule
)

// FieldError and Invalid are kernel's, so the HTTP layer answers a refused
// search as it answers any refused input.
type (
	FieldError = kernel.FieldError
	Invalid    = kernel.Invalid
)

// Lookup is the field of fields named name.
func Lookup(fields []Field, name string) (Field, bool) {
	i := slices.IndexFunc(fields, func(f Field) bool { return f.Name == name })
	if i < 0 {
		return Field{}, false
	}
	return fields[i], true
}

// Check refuses an input fields cannot answer: a predicate or order of a
// field they lack, a constraint that does not apply to its field's type,
// an order of a field that does not order, and a previous or next page
// without the cursor it reads from.
func Check(fields []Field, in Input) error {
	errs := checkPredicates(fields, in.Predicates)
	seen := map[string]bool{}
	for i, o := range in.OrderBy {
		at := "/orderBy/" + strconv.Itoa(i) + "/field"
		f, ok := Lookup(fields, o.Field)
		switch {
		case !ok:
			errs = append(errs, unknown(at, o.Field))
		case !f.Orderable:
			errs = append(errs, FieldError{
				Pointer: at, Reason: ReasonRule,
				Message: fmt.Sprintf("The field %s does not order a search.", o.Field),
			})
		case seen[o.Field]:
			errs = append(errs, FieldError{
				Pointer: at, Reason: ReasonDuplicate,
				Message: fmt.Sprintf("The search is already ordered by %s.", o.Field),
			})
		}
		seen[o.Field] = true
	}
	switch in.Page {
	case "", First, Last:
	case Next, Previous:
		if in.After == "" {
			errs = append(errs, FieldError{
				Pointer: "/after", Reason: ReasonRequired,
				Message: fmt.Sprintf("The %s page is read from a cursor.", in.Page),
			})
		}
	default:
		errs = append(errs, FieldError{
			Pointer: "/requestedPage", Reason: ReasonEnum,
			Message: fmt.Sprintf("%q is not a page.", in.Page),
		})
	}
	return invalid(errs)
}

// CheckSuggestions refuses suggestions whose predicates fields cannot
// answer, as Check does.
func CheckSuggestions(fields []Field, in SuggestionsInput) error {
	return invalid(checkPredicates(fields, in.Predicates))
}

func invalid(errs []FieldError) error {
	if len(errs) == 0 {
		return nil
	}
	return &Invalid{Detail: invalidDetail, Errors: errs}
}

// invalidDetail is the detail of every refused search.
const invalidDetail = "The search names fields or constraints this list does not have."

func checkPredicates(fields []Field, preds []Predicate) []FieldError {
	var errs []FieldError
	for i, p := range preds {
		at := "/predicates/" + strconv.Itoa(i)
		f, ok := Lookup(fields, p.Field)
		if !ok {
			errs = append(errs, unknown(at+"/field", p.Field))
			continue
		}
		errs = append(errs, checkConstraint(f, p.Constraint, at+"/constraint")...)
	}
	return errs
}

// applies names each constraint member and the type it applies to.
var applies = []struct {
	name string
	t    Type
	set  func(Constraint) bool
}{
	{"booleanEquals", Boolean, func(c Constraint) bool { return c.BooleanEquals != nil }},
	{"enumEquals", Enum, func(c Constraint) bool { return c.EnumEquals != nil }},
	{"stringEquals", String, func(c Constraint) bool { return c.StringEquals != nil }},
	{"stringMatches", String, func(c Constraint) bool { return c.StringMatches != nil }},
	{
		"hierarchyNodeValueEquals", Hierarchy,
		func(c Constraint) bool { return c.HierarchyValues != nil },
	},
	{"hierarchyNode", Hierarchy, func(c Constraint) bool { return c.Hierarchy != nil }},
	{"timeInRange", Time, func(c Constraint) bool { return c.TimeInRange != nil }},
	{"timeInLast", Time, func(c Constraint) bool { return c.TimeInLast != "" }},
}

// Kind is the name of c's member that is set, empty when none is or
// several are.
func (c Constraint) Kind() string {
	kind := ""
	for _, a := range applies {
		if a.set(c) {
			if kind != "" {
				return ""
			}
			kind = a.name
		}
	}
	return kind
}

func checkConstraint(f Field, c Constraint, at string) []FieldError {
	var set []int
	for i, a := range applies {
		if a.set(c) {
			set = append(set, i)
		}
	}
	if len(set) != 1 {
		return []FieldError{{Pointer: at, Reason: ReasonRule, Message: fmt.Sprintf(
			"A constraint sets exactly one member; this one sets %d.", len(set))}}
	}
	a := applies[set[0]]
	if a.t != f.Type {
		return []FieldError{{Pointer: at + "/" + a.name, Reason: ReasonRule, Message: fmt.Sprintf(
			"%s does not apply to %s, a %s field.", a.name, f.Name, f.Type)}}
	}
	var errs []FieldError
	for i, v := range c.EnumEquals {
		if !slices.Contains(f.Values, v) {
			errs = append(errs, FieldError{
				Pointer: at + "/enumEquals/" + strconv.Itoa(i), Reason: ReasonEnum,
				Message: fmt.Sprintf("%q is not a value of %s.", v, f.Name),
			})
		}
	}
	if _, ok := c.TimeInLast.Duration(); c.TimeInLast != "" && !ok {
		errs = append(errs, FieldError{
			Pointer: at + "/timeInLast", Reason: ReasonEnum,
			Message: fmt.Sprintf("%q is not a time window.", c.TimeInLast),
		})
	}
	return errs
}

func unknown(at, name string) FieldError {
	return FieldError{
		Pointer: at,
		Reason:  ReasonUnknownField,
		Message: fmt.Sprintf("The search has no field %q.", name),
	}
}

// Edge is a row of a page and the cursor that reads on from it.
type Edge[T any] struct {
	Cursor string
	Node   T
}

// Result is a page of a search: its edges, the cursors of the first and
// last, empty when it has none, and whether pages lie either side.
type Result[T any] struct {
	Edges       []Edge[T]
	Start, End  string
	HasNext     bool
	HasPrevious bool
}

// Suggestions are an entity's fields with the values the rows a
// SuggestionsInput keeps hold, and how many rows it keeps.
type Suggestions struct {
	Count  int
	Fields []Suggestion
}

// Suggestion is a field and, when asked for, its values: the most common
// first, each with how many kept rows hold it. A HIERARCHY field's values
// are Nodes, each counting the rows at it and below it, and Direct the rows
// at it alone; a TIME field lists none.
type Suggestion struct {
	Field  Field
	Listed bool
	Values []string
	Nodes  []HierarchyNode
	Counts []int
	Direct []int
}

// HierarchyNode is a value of a HIERARCHY field: its ID, its parent's,
// empty at a root, and what it shows as.
type HierarchyNode struct {
	ID, Parent, Display string
}
