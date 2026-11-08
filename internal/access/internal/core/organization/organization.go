package organization

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// MaxSlug is the longest slug: a DNS label's 63 characters, so an
// organization or account slug can name a host as Spacelift's account
// names do.
const MaxSlug = 63

// MaxName is the longest display name, in characters.
const MaxName = 100

// reserved are slugs no organization or account may take. root is the
// installation's top node, and a top space named root could not say which
// account it belongs to; legacy is the slug of the space below every top
// space, which takes its account's slug, so an account named legacy would
// hold two spaces of one slug.
//
// ref: decision 0007 § "Tenancy"
var reserved = map[string]bool{"root": true, "legacy": true}

// ErrInvalid marks input that breaks a rule; the error names the field.
var ErrInvalid = errors.New("invalid")

// FieldError is input that breaks a rule of one field, which a handler
// answers with 422 validation-failed naming the field.
//
// ref: decision 0022
type FieldError struct {
	Field  string
	Reason string
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Reason }

// Is makes every FieldError match ErrInvalid.
func (e *FieldError) Is(target error) bool { return target == ErrInvalid }

// ParseSlug checks an organization or account slug: 1 to 63 lower-case
// letters, digits and single hyphens, starting with a letter and ending
// with a letter or digit, and not a reserved word. A slug becomes part of
// external names (the registry namespace, the federation audience, AWS
// external IDs), so its alphabet is the narrowest they all accept.
func ParseSlug(field, s string) (kernel.Slug, error) {
	switch {
	case s == "":
		return "", &FieldError{field, "is empty"}
	case len(s) > MaxSlug:
		return "", &FieldError{field, fmt.Sprintf("is longer than %d characters", MaxSlug)}
	case s[0] < 'a' || s[0] > 'z':
		return "", &FieldError{field, "does not start with a lower-case letter"}
	case s[len(s)-1] == '-':
		return "", &FieldError{field, "ends with a hyphen"}
	case strings.Contains(s, "--"):
		return "", &FieldError{field, "has two hyphens in a row"}
	case reserved[s]:
		return "", &FieldError{field, fmt.Sprintf("%q is reserved", s)}
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return "", &FieldError{field, fmt.Sprintf("has %q; only a-z, 0-9 and - are allowed", r)}
		}
	}
	return kernel.Slug(s), nil
}

// CheckName checks a display name: not blank, and at most MaxName
// characters.
func CheckName(field, s string) error {
	switch {
	case strings.TrimSpace(s) == "":
		return &FieldError{field, "is empty"}
	case utf8.RuneCountInString(s) > MaxName:
		return &FieldError{field, fmt.Sprintf("is longer than %d characters", MaxName)}
	}
	return nil
}

// Organization is a customer's agreement with the installation (decision
// 0007). Its slug never changes; its name may.
type Organization struct {
	ID        kernel.OrganizationID
	Slug      kernel.Slug
	Name      string
	CreatedAt time.Time
}

// New checks a new organization's slug and name and gives it an ID.
func New(slug, name string) (Organization, error) {
	s, serr := ParseSlug("slug", slug)
	nerr := CheckName("name", name)
	if err := errors.Join(serr, nerr); err != nil {
		return Organization{}, err
	}
	return Organization{ID: kernel.NewOrganizationID(), Slug: s, Name: name}, nil
}
