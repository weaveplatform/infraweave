package kernel

import (
	"fmt"
	"strconv"
	"unicode"
	"unicode/utf8"
)

// The bounds of an entity's labels.
//
// ref: decision 0022
const (
	MaxLabels      = 100
	MaxLabelLength = 255
)

// CheckLabels refuses each label that breaks the label rule, at its
// pointer below at: 1 to MaxLabelLength characters of UTF-8, with no
// control character and no whitespace at either end, and none repeated,
// comparing bytes, so Prod and prod are two labels. More than MaxLabels
// is refused at at itself. Prefixes with a meaning, such as autoattach:,
// obey the same rule.
func CheckLabels(at string, labels []string) []FieldError {
	var errs []FieldError
	if len(labels) > MaxLabels {
		errs = append(errs, FieldError{
			Pointer: at, Reason: ReasonMaxLength,
			Message: fmt.Sprintf("at most %d labels", MaxLabels),
		})
	}
	seen := make(map[string]bool, len(labels))
	for i, l := range labels {
		ptr := at + "/" + strconv.Itoa(i)
		reason, msg := labelFault(l)
		if reason == "" && seen[l] {
			reason, msg = ReasonDuplicate, fmt.Sprintf("the label %q is repeated", l)
		}
		seen[l] = true
		if reason != "" {
			errs = append(errs, FieldError{Pointer: ptr, Reason: reason, Message: msg})
		}
	}
	return errs
}

// labelFault is the reason and message of the rule l breaks, or empty.
func labelFault(l string) (reason, msg string) {
	switch n := utf8.RuneCountInString(l); {
	case !utf8.ValidString(l):
		return ReasonFormat, "a label is UTF-8"
	case n == 0:
		return ReasonMinLength, "a label is not empty"
	case n > MaxLabelLength:
		return ReasonMaxLength, fmt.Sprintf("a label is at most %d characters", MaxLabelLength)
	}
	for _, r := range l {
		if unicode.IsControl(r) {
			return ReasonPattern, "a label holds no control character"
		}
	}
	first, _ := utf8.DecodeRuneInString(l)
	last, _ := utf8.DecodeLastRuneInString(l)
	if unicode.IsSpace(first) || unicode.IsSpace(last) {
		return ReasonPattern, "a label has no whitespace at either end"
	}
	return "", ""
}
