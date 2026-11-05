package cron

import (
	"errors"
	"fmt"
	"strings"
)

// ErrSyntax marks an expression the grammar rejects. Its message names the
// field at fault, so an API can report it as the reason.
var ErrSyntax = errors.New("invalid cron expression")

// problem is why a field is rejected; Parse wraps it in ErrSyntax with the
// field's name.
type problem string

func (p problem) Error() string { return string(p) }

// A bound is one field's name and range. Day of week runs to 7 so that both
// 0 and 7 are Sunday.
type bound struct {
	name     string
	min, max int
}

var bounds = [5]bound{
	{"minute", 0, 59},
	{"hour", 0, 23},
	{"day of month", 1, 31},
	{"month", 1, 12},
	{"day of week", 0, 7},
}

const (
	minute = iota
	hour
	dayOfMonth
	month
	dayOfWeek
)

// field is one parsed field: the values it matches as bits, and its
// canonical text.
type field struct {
	bits uint64
	text string
}

// Schedule is a parsed five-field expression. Schedules compare with ==,
// and equal schedules have equal String forms.
type Schedule struct {
	fields [5]field
}

// String is the canonical form: single spaces between fields and numbers
// without leading zeros. Parse accepts it and returns an equal Schedule.
func (s Schedule) String() string {
	texts := make([]string, len(s.fields))
	for i, f := range s.fields {
		texts[i] = f.text
	}
	return strings.Join(texts, " ")
}

// Parse reads a five-field expression: minute, hour, day of month, month
// and day of week. Each field is a comma list of items, each `*`, `n` or
// `a-b`, optionally followed by `/step`; `n/step` runs from n to the
// field's maximum. Names, `?`, `L`, `W`, `#`, `@` macros, seconds and year
// fields, values out of range, reversed ranges and a zero step are
// rejected with ErrSyntax, as is a day of month that no selected month has.
func Parse(expr string) (Schedule, error) {
	parts := strings.Fields(expr)
	if len(parts) > 0 && strings.HasPrefix(parts[0], "@") {
		return Schedule{}, fmt.Errorf("%w: %q: @ macros are not supported; write the five fields",
			ErrSyntax, parts[0])
	}
	if len(parts) != len(bounds) {
		return Schedule{}, fmt.Errorf(
			"%w: want five fields (minute, hour, day of month, month, day of week), got %d",
			ErrSyntax, len(parts))
	}
	var s Schedule
	for i, p := range parts {
		f, err := parseField(p, bounds[i])
		if err != nil {
			return Schedule{}, fmt.Errorf("%w: %s: %w", ErrSyntax, bounds[i].name, err)
		}
		s.fields[i] = f
	}
	// 7 is Sunday, as 0 is.
	if s.fields[dayOfWeek].bits&(1<<7) != 0 {
		s.fields[dayOfWeek].bits = s.fields[dayOfWeek].bits&^(1<<7) | 1
	}
	// With every weekday allowed, day of month alone picks the days.
	if !s.restricted(dayOfWeek) && !s.dayExists() {
		return Schedule{}, fmt.Errorf("%w: day of month: %s never occurs in month %s",
			ErrSyntax, s.fields[dayOfMonth].text, s.fields[month].text)
	}
	return s, nil
}

// daysIn is each month's longest length, February's in a leap year.
var daysIn = [13]int{0, 31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}

// dayExists reports whether a selected day of month occurs in a selected
// month, so a schedule matched by day of month alone fires at all.
func (s Schedule) dayExists() bool {
	for m := 1; m <= 12; m++ {
		if s.fields[month].bits&(1<<m) == 0 {
			continue
		}
		for d := 1; d <= daysIn[m]; d++ {
			if s.fields[dayOfMonth].bits&(1<<d) != 0 {
				return true
			}
		}
	}
	return false
}

// full is the bits of every value of field i; for day of week, 0 to 6.
func full(i int) uint64 {
	b := bounds[i]
	if i == dayOfWeek {
		return 1<<7 - 1
	}
	return (1<<(b.max+1) - 1) &^ (1<<b.min - 1)
}

// restricted reports whether field i leaves out any of its values. Day of
// month and day of week combine by "either matches" only when both are.
func (s Schedule) restricted(i int) bool { return s.fields[i].bits != full(i) }

func parseField(text string, b bound) (field, error) {
	var (
		f     field
		items []string
	)
	for item := range strings.SplitSeq(text, ",") {
		bits, canon, err := parseItem(item, b)
		if err != nil {
			return field{}, err
		}
		f.bits |= bits
		items = append(items, canon)
	}
	f.text = strings.Join(items, ",")
	return f, nil
}

// parseItem reads `*`, `n` or `a-b`, each with an optional `/step`.
func parseItem(item string, b bound) (uint64, string, error) {
	rng, stepText, stepped := strings.Cut(item, "/")
	step := 1
	if stepped {
		n, err := number(stepText, "step")
		if err != nil {
			return 0, "", err
		}
		if n < 1 || n > b.max-b.min+1 {
			return 0, "", problem(fmt.Sprintf("step %d is outside 1-%d", n, b.max-b.min+1))
		}
		step = n
	}
	lo, hi := b.min, b.max
	var canon string
	switch first, last, isRange := strings.Cut(rng, "-"); {
	case rng == "*":
		canon = "*"
	case isRange:
		var err error
		if lo, err = value(first, b); err != nil {
			return 0, "", err
		}
		if hi, err = value(last, b); err != nil {
			return 0, "", err
		}
		if lo > hi {
			return 0, "", problem(fmt.Sprintf("range %d-%d is reversed", lo, hi))
		}
		canon = fmt.Sprintf("%d-%d", lo, hi)
	default:
		var err error
		if lo, err = value(rng, b); err != nil {
			return 0, "", err
		}
		canon = fmt.Sprint(lo)
		if !stepped {
			hi = lo
		}
	}
	if stepped {
		canon += fmt.Sprintf("/%d", step)
	}
	var bits uint64
	for v := lo; v <= hi; v += step {
		bits |= 1 << v
	}
	return bits, canon, nil
}

// value is a number within the field's range.
func value(text string, b bound) (int, error) {
	n, err := number(text, "value")
	if err != nil {
		return 0, err
	}
	if n < b.min || n > b.max {
		return 0, problem(fmt.Sprintf("%d is outside %d-%d", n, b.min, b.max))
	}
	return n, nil
}

// number reads decimal digits only, so signs, names and the Quartz
// characters ?, L, W and # are rejected. Values longer than three digits
// are out of every range, which also keeps the arithmetic from overflowing.
func number(text, what string) (int, error) {
	if text == "" {
		return 0, problem("empty " + what)
	}
	n := 0
	for _, r := range text {
		if r < '0' || r > '9' {
			return 0, problem(fmt.Sprintf(
				"%s %q is not a number; names, ?, L, W and # are not supported", what, text))
		}
		if n > 999 {
			continue
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}
