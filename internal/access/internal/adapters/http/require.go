package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	accessapi "github.com/weaveplatform/infraweave/internal/access/api"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/httpkit/problem"
)

// bodyPeek is how much of a request body a body subject reads for its
// field. A create's body is far smaller; one larger decides on the top
// space, which no caller can be allowed more on than on the space it
// names.
const bodyPeek = 1 << 20

// Subjects are the space lookups of each kind of entity, which the
// domains that own them register before the API serves.
type Subjects struct {
	mu     sync.RWMutex
	lookup map[string]accessapi.SpaceOf
}

// NewSubjects returns an empty set of lookups.
func NewSubjects() *Subjects { return &Subjects{lookup: map[string]accessapi.SpaceOf{}} }

// Register makes f the lookup of entity's spaces.
func (s *Subjects) Register(entity string, f accessapi.SpaceOf) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lookup[entity] = f
}

func (s *Subjects) of(entity string) (accessapi.SpaceOf, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, ok := s.lookup[entity]
	return f, ok
}

// Require is accessapi.Requirer over an authorizer and the subjects'
// lookups.
type Require struct {
	authz    accessapi.Authorizer
	subjects *Subjects
}

var _ accessapi.Requirer = Require{}

// NewRequire returns the middleware source deciding with authz, finding
// entities' spaces through subjects.
func NewRequire(authz accessapi.Authorizer, subjects *Subjects) Require {
	return Require{authz: authz, subjects: subjects}
}

// Middleware decides each request require names an operation for, by
// its subject:
//   - account decides the action on the account's top space, and any in
//     the space where the caller holds it, answering 403 when it may not;
//   - organization and list pass: the organization's own rule decides an
//     organization operation, and a list's handler filters with
//     Reachable;
//   - a path, body or query subject finds the entity's space, answering
//     404 for an entity the account lacks or one whose space the caller
//     cannot read, with one body for both, then 403 when it may not
//     perform the action there. An entity kind no domain registers answers 501, as no
//     domain serves its operations.
//
// A request with no principal passes, as only a public route has none.
func (q Require) Middleware(
	require func(*http.Request) (accessapi.Required, bool),
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req, ok := require(r)
			p, authenticated := kernel.PrincipalFrom(r.Context())
			if !ok || !authenticated {
				next.ServeHTTP(w, r)
				return
			}
			allowed, answered := q.decide(w, r, p, req)
			if !answered && allowed {
				next.ServeHTTP(w, r)
				return
			}
			if !answered {
				forbid(w, req.Action)
			}
		})
	}
}

// decide reports whether p may make r, or answers r itself, reporting
// answered, for a subject that is missing or hidden or a failed check.
func (q Require) decide(
	w http.ResponseWriter,
	r *http.Request,
	p kernel.Principal,
	req accessapi.Required,
) (allowed, answered bool) {
	ctx := r.Context()
	var err error
	switch req.Subject.Kind {
	case accessapi.SubjectOrganization, accessapi.SubjectList:
		return true, false
	case accessapi.SubjectAccount:
		allowed, err = q.authz.Authorize(ctx, p, req.Action, p.AccountID.ULID)
	case accessapi.SubjectAny:
		allowed, err = q.authz.Holds(ctx, p, req.Action)
	default:
		return q.decideEntity(w, r, p, req)
	}
	if err != nil {
		problem.Failure(w, err)
		return false, true
	}
	return allowed, false
}

// decideEntity decides a path, body or query subject. A query subject
// whose parameter is absent decides on the top space, as a body subject
// without its field does.
func (q Require) decideEntity(
	w http.ResponseWriter,
	r *http.Request,
	p kernel.Principal,
	req accessapi.Required,
) (allowed, answered bool) {
	ctx := r.Context()
	s := req.Subject
	lookup, ok := q.subjects.of(s.Entity)
	if !ok {
		problem.Write(w, problem.NotImplemented.New(fmt.Sprintf(
			"No domain serves the %s this operation acts on yet.", s.Entity)))
		return false, true
	}
	var id string
	switch s.Kind {
	case accessapi.SubjectBody:
		id = bodyField(r, s.Field)
	case accessapi.SubjectQuery:
		id = r.URL.Query().Get(s.Param)
	default:
		id = req.Params[s.Param]
	}
	space := p.AccountID.ULID
	if id != "" {
		var err error
		space, err = lookup(ctx, p.AccountID, id)
		switch {
		case errors.Is(err, accessapi.ErrNoSubject):
			hidden(w, s.Entity, id)
			return false, true
		case err != nil:
			problem.Failure(w, err)
			return false, true
		}
	}
	read, err := q.authz.Authorize(ctx, p, accessapi.ActionSpaceRead, space)
	if err == nil && read {
		allowed, err = q.authz.Authorize(ctx, p, req.Action, space)
	}
	switch {
	case err != nil:
		problem.Failure(w, err)
		return false, true
	case !read:
		hidden(w, s.Entity, id)
		return false, true
	}
	return allowed, false
}

// bodyField is the string at the dotted field of r's JSON body, or empty
// when the body holds none, which decides on the top space. The body is
// left whole for the handler.
func bodyField(r *http.Request, field string) string {
	if r.Body == nil {
		return ""
	}
	head, err := io.ReadAll(io.LimitReader(r.Body, bodyPeek+1))
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(head), r.Body), r.Body}
	if err != nil || len(head) > bodyPeek {
		return ""
	}
	var v any
	if json.Unmarshal(head, &v) != nil {
		return ""
	}
	for name := range strings.SplitSeq(field, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return ""
		}
		v = m[name]
	}
	s, _ := v.(string)
	return s
}

// hidden answers 404 for an entity that is missing or that the caller
// cannot read, with the same detail for both (decision 0022).
func hidden(w http.ResponseWriter, entity, id string) {
	problem.Write(w, problem.NotFound.New(fmt.Sprintf("No %s %q in this account.",
		strings.ReplaceAll(entity, "_", " "), id)))
}

// forbid answers 403 naming the action the caller may not perform.
func forbid(w http.ResponseWriter, action accessapi.Action) {
	p := problem.Forbidden.New(fmt.Sprintf("You may not perform %s here.", action))
	p.Action = string(action)
	problem.Write(w, p)
}
