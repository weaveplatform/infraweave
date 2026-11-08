package api

import (
	"errors"
	"fmt"
	"strings"
)

// SubjectKind is where an operation's x-infraweave-subject finds the space
// its action is decided in (decision 0007 § "Enforcement point").
type SubjectKind string

const (
	// SubjectPath is the space of the entity a path parameter names:
	// path:<param>:<entity>.
	SubjectPath SubjectKind = "path"
	// SubjectBody is the space of the entity a request body field names,
	// a space unless the field's entity says otherwise:
	// body:<field>[:<entity>], the field dotted through nested objects.
	SubjectBody SubjectKind = "body"
	// SubjectAccount is the account's top space.
	SubjectAccount SubjectKind = "account"
	// SubjectAny is any space of the account: the action is allowed where
	// the caller holds it in at least one, as an anywhere action is.
	SubjectAny SubjectKind = "any"
	// SubjectOrganization is no space: the operation acts on the caller's
	// organization, and the organization's own rule decides it.
	SubjectOrganization SubjectKind = "organization"
	// SubjectList is no space: the handler filters what it lists with
	// Reachable.
	SubjectList SubjectKind = "list"
)

// EntitySpace is the entity a body subject names when it names none.
const EntitySpace = "space"

// Subject is a parsed x-infraweave-subject. Param is a path subject's
// parameter, Field a body subject's dotted field, and Entity the kind of
// entity either names.
type Subject struct {
	Kind   SubjectKind
	Param  string
	Field  string
	Entity string
}

// ErrSubject is an x-infraweave-subject the grammar does not hold.
var ErrSubject = errors.New("invalid x-infraweave-subject")

// ParseSubject reads s: account, any, organization, list,
// path:<param>:<entity> or body:<field>[:<entity>].
func ParseSubject(s string) (Subject, error) {
	parts := strings.Split(s, ":")
	for _, p := range parts {
		if p == "" {
			return Subject{}, fmt.Errorf("%w %q", ErrSubject, s)
		}
	}
	kind := SubjectKind(parts[0])
	switch {
	case len(parts) == 1 && (kind == SubjectAccount || kind == SubjectAny ||
		kind == SubjectOrganization || kind == SubjectList):
		return Subject{Kind: kind}, nil
	case kind == SubjectPath && len(parts) == 3:
		return Subject{Kind: kind, Param: parts[1], Entity: parts[2]}, nil
	case kind == SubjectBody && len(parts) == 2:
		return Subject{Kind: kind, Field: parts[1], Entity: EntitySpace}, nil
	case kind == SubjectBody && len(parts) == 3:
		return Subject{Kind: kind, Field: parts[1], Entity: parts[2]}, nil
	}
	return Subject{}, fmt.Errorf("%w %q", ErrSubject, s)
}

// Names reports whether the subject names an entity, whose space it
// resolves before deciding.
func (s Subject) Names() bool { return s.Kind == SubjectPath || s.Kind == SubjectBody }
