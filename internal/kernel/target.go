package kernel

import (
	"fmt"
	"regexp"
	"unicode/utf8"
)

// MaxTargets bounds the addresses of one targeted replan, and MaxTarget
// each address, in characters: runTargetedReplan's targets and the run
// specification's targets have them as their maxItems and maxLength.
//
// ref: decision 0003 § "Bounds in the domains"
const (
	MaxTargets = 100
	MaxTarget  = 1024
)

// target matches a resource or module address as the Terraform family's
// -target takes it: module steps, each with an optional key, then
// optionally a resource, data. or not, with an optional key. A key is an
// integer or a quoted string with no control character. The runner quotes
// each address as one shell word, so the match keeps out a typo, not
// shell syntax: a quoted key may hold any other character.
var target = regexp.MustCompile(
	`^(?:module\.` + ident + key + `\.)*(?:module\.` + ident + key +
		`|(?:data\.)?` + ident + `\.` + ident + key + `)$`)

// The parts of an address target matches: an identifier, as HCL writes
// one, and an optional instance key.
const (
	ident = `[A-Za-z_][A-Za-z0-9_-]*`
	key   = `(?:\[(?:[0-9]+|"(?:[^"\\\x00-\x1f\x7f]|\\[^\x00-\x1f\x7f])*")\])?`
)

// CheckTargets refuses targets at at when there are none or more than
// MaxTargets, and each address at at/<i> that is longer than MaxTarget or
// is not a resource or module address.
//
// ref: decision 0008 § "The run specification and its secrets"
func CheckTargets(at string, targets []string) []FieldError {
	switch {
	case len(targets) == 0:
		return []FieldError{{
			Pointer: at, Reason: ReasonMinLength, Message: "A replan names at least one address.",
		}}
	case len(targets) > MaxTargets:
		return []FieldError{{
			Pointer: at, Reason: ReasonMaxLength,
			Message: fmt.Sprintf("A replan names at most %d addresses.", MaxTargets),
		}}
	}
	var out []FieldError
	for i, t := range targets {
		ptr := fmt.Sprintf("%s/%d", at, i)
		switch {
		case utf8.RuneCountInString(t) > MaxTarget:
			out = append(out, FieldError{
				Pointer: ptr, Reason: ReasonMaxLength,
				Message: fmt.Sprintf("An address is at most %d characters.", MaxTarget),
			})
		case !target.MatchString(t):
			out = append(out, FieldError{
				Pointer: ptr, Reason: ReasonRule,
				Message: "An address names a resource or a module, such as " +
					`aws_instance.web or module.app["a"].`,
			})
		}
	}
	return out
}
