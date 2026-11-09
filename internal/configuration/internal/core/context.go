package core

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	stacksapi "github.com/weaveplatform/infraweave/internal/stacks/api"
)

// Context is a context as the domain keeps it. Its slug is made from its
// first name and never changes; Hooks maps each phase that has commands
// to them, in order. Version counts its writes, and every change to its
// elements and attachments is one.
type Context struct {
	ID          kernel.ULID
	Account     kernel.AccountID
	Slug        string
	Space       kernel.ULID
	Name        string
	Description *string
	Labels      []string
	Hooks       map[string][]string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Version     int64
}

// Owner is what holds a config element: a context or a stack, the other
// zero.
type Owner struct {
	Context kernel.ULID
	Stack   kernel.ULID
}

// Element is a config element: an environment variable, or a file mounted
// in the workspace when FileMount is set. ID is its row; Name is the
// variable's name or the file's path, which the API names it by. Value is
// its value, the file's content in Base64 for a file; a read leaves it nil
// when WriteOnly is set, and a write of nil keeps the value the element
// has. Checksum is the value's SHA-256 in hex.
type Element struct {
	ID          kernel.ULID
	Account     kernel.AccountID
	Owner       Owner
	Name        string
	FileMount   bool
	Value       *string
	Checksum    string
	WriteOnly   bool
	Description string
	FileMode    *string
	CreatedAt   time.Time
}

// Attachment is a context's attachment to a stack. Contexts attached to a
// stack sort by Priority, lowest first; Autoattached marks one an
// autoattach label made.
type Attachment struct {
	ID           kernel.ULID
	Account      kernel.AccountID
	Context      kernel.ULID
	Stack        kernel.ULID
	Priority     int32
	Autoattached bool
	CreatedAt    time.Time
}

// Input is a context's fields as a write gives them, with its space by
// slug, empty for the account's top space. Config is the input's
// configAttachments, nil when it has none, and Stacks its stack
// attachments.
type Input struct {
	Space       string
	Name        string
	Description *string
	Labels      []string
	Hooks       map[string][]string
	Config      []ElementInput
	Stacks      []StackInput
}

// ElementInput is a config element as a write gives it. A nil Value keeps
// the value of the element it replaces.
type ElementInput struct {
	Name        string
	FileMount   bool
	Value       *string
	WriteOnly   bool
	Description *string
	FileMode    *string
}

// StackInput attaches the context to the stack slug at Priority.
type StackInput struct {
	Stack    string
	Priority int32
}

// MaxFile is the largest file a file mount's value decodes to.
const MaxFile = 2 << 20

// invalidDetail is the detail of an input the checks refuse.
const invalidDetail = "The context's settings are refused."

// fileMode is a file mount's mode, in octal.
var fileMode = regexp.MustCompile(`^[0-7]{3,4}$`)

// check refuses each field of in a context cannot hold, with pointers
// under at, the input's place in the request body.
func check(at string, in Input) error {
	var errs []kernel.FieldError
	switch {
	case strings.TrimSpace(in.Name) == "":
		errs = append(errs, kernel.FieldError{
			Pointer: at + "/name", Reason: kernel.ReasonRequired,
			Message: "A context needs a name.",
		})
	case !kernel.SlugFrom(in.Name).Valid():
		errs = append(errs, kernel.FieldError{
			Pointer: at + "/name",
			Reason:  kernel.ReasonRule,
			Message: "A context's name needs a letter a to z or a digit, from which its slug is made.",
		})
	}
	errs = append(errs, kernel.CheckLabels(at+"/labels", in.Labels)...)
	seen := map[string]bool{}
	for i, e := range in.Config {
		ptr := fmt.Sprintf("%s/configAttachments/%d", at, i)
		errs = append(errs, checkElement(ptr, "/id", e)...)
		if e.Value == nil {
			errs = append(errs, kernel.FieldError{
				Pointer: ptr + "/value",
				Reason:  kernel.ReasonRequired, Message: "A config element needs a value.",
			})
		}
		if seen[e.Name] {
			errs = append(errs, kernel.FieldError{
				Pointer: ptr + "/id", Reason: kernel.ReasonDuplicate,
				Message: fmt.Sprintf("Config element %q is given twice.", e.Name),
			})
		}
		seen[e.Name] = true
	}
	if len(errs) == 0 {
		return nil
	}
	return &kernel.Invalid{Detail: invalidDetail, Errors: errs}
}

// checkElement refuses each field of e an element cannot hold, with
// pointers under at, and the element's name at at+name.
func checkElement(at, name string, e ElementInput) []kernel.FieldError {
	var errs []kernel.FieldError
	refuse := func(field, reason, message string) {
		errs = append(
			errs,
			kernel.FieldError{Pointer: at + field, Reason: reason, Message: message},
		)
	}
	switch {
	case e.Name == "":
		refuse(name, kernel.ReasonRequired, "A config element needs an ID.")
	case strings.ContainsRune(e.Name, 0):
		refuse(name, kernel.ReasonRule, "A config element's ID holds no NUL character.")
	case !e.FileMount && strings.Contains(e.Name, "="):
		refuse(name, kernel.ReasonRule, "An environment variable's name holds no =.")
	case e.FileMount && !relative(e.Name):
		refuse(name, kernel.ReasonRule, "A mounted file's path is relative to the workspace, "+
			"with no empty, . or .. part.")
	}
	if e.FileMode != nil && !e.FileMount {
		refuse("/fileMode", kernel.ReasonRule, "Only a mounted file has a file mode.")
	} else if e.FileMode != nil && !fileMode.MatchString(*e.FileMode) {
		refuse("/fileMode", kernel.ReasonPattern, "A file mode is three or four octal digits.")
	}
	if e.Value == nil {
		return errs
	}
	if !e.FileMount {
		if strings.ContainsRune(*e.Value, 0) {
			refuse(
				"/value",
				kernel.ReasonRule,
				"An environment variable's value holds no NUL character.",
			)
		}
		return errs
	}
	content, err := base64.StdEncoding.DecodeString(*e.Value)
	switch {
	case err != nil:
		refuse("/value", kernel.ReasonFormat, "A mounted file's value is its content in Base64.")
	case len(content) > MaxFile:
		refuse("/value", kernel.ReasonMaxLength, "A mounted file is at most 2 MB.")
	}
	return errs
}

// relative reports whether p is a path below the workspace: no leading /,
// and no empty, . or .. part, so it names one file there.
func relative(p string) bool {
	if strings.HasPrefix(p, "/") || path.Clean(p) != p {
		return false
	}
	return !slices.ContainsFunc(strings.Split(p, "/"), func(part string) bool {
		return part == "" || part == "." || part == ".."
	})
}

// checksum is v's SHA-256 in hex.
func checksum(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}

// apply sets c's fields from in, the space's ID already resolved, as a
// PUT replaces them: a field in leaves out takes its default.
func apply(c *Context, space kernel.ULID, in Input) {
	c.Space = space
	c.Name = strings.TrimSpace(in.Name)
	c.Description = in.Description
	c.Labels = slices.Sorted(slices.Values(in.Labels))
	if c.Labels == nil {
		c.Labels = []string{}
	}
	c.Hooks = map[string][]string{}
	for _, phase := range stacksapi.HookPhases {
		if cmds := in.Hooks[phase]; len(cmds) > 0 {
			c.Hooks[phase] = slices.Clone(cmds)
		}
	}
}
