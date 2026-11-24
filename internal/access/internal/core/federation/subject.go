package federation

import (
	"errors"
	"fmt"
	"strings"
)

// DefaultSubjectTemplate is the template of an account that set none.
const DefaultSubjectTemplate = "space:{spaceId}:{callerType}:{callerId}:run_type:{runType}:scope:{scope}"

// The bounds on a template and on the subject it renders.
const (
	MaxTemplate = 1000
	MaxSubject  = 2048
)

// ErrInvalid reports a template, or a rendered subject, the bounds refuse;
// the error names the offending part.
var ErrInvalid = errors.New("invalid OIDC subject template")

// Caller is what a run's token says about the run, from which a subject is
// rendered.
type Caller struct {
	// SpaceID is the space's ID; SpacePath is the IDs of the spaces from
	// the top space down to it, joined by "/".
	SpaceID, SpacePath string
	// Type is "stack" or "module", and ID its slug.
	Type, ID string
	RunID    string
	RunType  string
	// Scope is "read" or "write".
	Scope string
}

// placeholders are the names a template may use, each rendered from a
// Caller.
var placeholders = map[string]func(Caller) string{
	"spaceId":    func(c Caller) string { return c.SpaceID },
	"spacePath":  func(c Caller) string { return c.SpacePath },
	"callerType": func(c Caller) string { return c.Type },
	"callerId":   func(c Caller) string { return c.ID },
	"runId":      func(c Caller) string { return c.RunID },
	"runType":    func(c Caller) string { return c.RunType },
	"scope":      func(c Caller) string { return c.Scope },
}

// CheckSubjectTemplate checks t: at most MaxTemplate characters of
// letters, digits and "-_:/|", with placeholders such as {spaceId} as the
// only braces. Characters such as spaces, "&", "=", "?", "#", "@" and "%"
// are refused because cloud trust policies match the subject as written.
// An empty t is the default and passes.
func CheckSubjectTemplate(t string) error {
	_, err := render(t, Caller{})
	return err
}

// RenderSubject is template t, or DefaultSubjectTemplate when t is empty,
// with each placeholder replaced from c. A subject longer than MaxSubject
// is ErrInvalid.
func RenderSubject(t string, c Caller) (string, error) {
	if t == "" {
		t = DefaultSubjectTemplate
	}
	s, err := render(t, c)
	if err != nil {
		return "", err
	}
	if len(s) > MaxSubject {
		return "", fmt.Errorf("%w: the subject is %d characters, more than %d", ErrInvalid, len(s), MaxSubject)
	}
	return s, nil
}

// render checks t's length and walks it once, checking each character and
// replacing each placeholder from c.
func render(t string, c Caller) (string, error) {
	if len(t) > MaxTemplate {
		return "", fmt.Errorf("%w: the template is %d characters, more than %d", ErrInvalid, len(t), MaxTemplate)
	}
	var b strings.Builder
	for i := 0; i < len(t); i++ {
		ch := t[i]
		switch {
		case ch == '{':
			end := strings.IndexByte(t[i:], '}')
			if end < 0 {
				return "", fmt.Errorf("%w: the brace at %d is not closed", ErrInvalid, i)
			}
			name := t[i+1 : i+end]
			value, ok := placeholders[name]
			if !ok {
				return "", fmt.Errorf("%w: unknown placeholder {%s}", ErrInvalid, name)
			}
			b.WriteString(value(c))
			i += end
		case ch == '}':
			return "", fmt.Errorf("%w: the brace at %d closes no placeholder", ErrInvalid, i)
		case allowed(ch):
			b.WriteByte(ch)
		default:
			return "", fmt.Errorf("%w: character %q at %d is not allowed", ErrInvalid, rune(ch), i)
		}
	}
	return b.String(), nil
}

// allowed reports whether ch may appear in a template outside a
// placeholder.
func allowed(ch byte) bool {
	switch {
	case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9':
		return true
	}
	return strings.IndexByte("-_:/|", ch) >= 0
}
