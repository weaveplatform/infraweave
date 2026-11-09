package version

import (
	"cmp"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Op is a constraint's operator. The empty Op is =.
type Op string

// The operators a constraint may use; ~> allows only the rightmost
// segment it names to grow, so ~> 1.5 allows 1.9 and ~> 1.5.0 allows
// 1.5.9 but not 1.6.0.
const (
	Exact        Op = ""
	Equal        Op = "="
	NotEqual     Op = "!="
	Greater      Op = ">"
	GreaterEqual Op = ">="
	Less         Op = "<"
	LessEqual    Op = "<="
	Pessimistic  Op = "~>"
)

// Version is a release number: one to three numeric segments, then an
// optional prerelease after - and build metadata after +, as in
// 1.6.0-beta1+abc.
type Version struct {
	Segments   []int64
	Prerelease string
	Metadata   string
}

// Constraint is one operator and the version it compares with.
type Constraint struct {
	Op      Op
	Version Version
}

// Constraints are a version field's comma-separated constraints, all of
// which a release must meet.
type Constraints []Constraint

// MaxSegments is the most numeric segments a version may have: Terraform
// and OpenTofu number their releases major.minor.patch, so a fourth
// segment names no release.
const MaxSegments = 3

// The grammar of Terraform's and OpenTofu's version constraints, which
// stacks share so that a version the tool would refuse is refused when the
// stack is written. An identifier of a prerelease or metadata is letters,
// digits, - and ~, and dots separate identifiers.
var (
	constraintPattern = regexp.MustCompile(`^(=|!=|>=|>|<=|<|~>)?\s*(.*)$`)
	versionPattern    = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)*)` +
		`(?:-([0-9A-Za-z~-]+(?:\.[0-9A-Za-z~-]+)*))?` +
		`(?:\+([0-9A-Za-z~-]+(?:\.[0-9A-Za-z~-]+)*))?$`)
)

// ErrSyntax marks a constraint or version that does not parse; the error
// says which part.
var ErrSyntax = errors.New("not a version constraint")

// ParseConstraints parses s: constraints separated by commas, each an
// optional operator, optional spaces and a version, such as
// ">= 1.5, < 2.0" or "~> 1.6.0".
func ParseConstraints(s string) (Constraints, error) {
	if strings.TrimSpace(s) == "" {
		return nil, fmt.Errorf("%w: it is empty", ErrSyntax)
	}
	parts := strings.Split(s, ",")
	out := make(Constraints, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("%w: %q has an empty constraint between commas", ErrSyntax, s)
		}
		m := constraintPattern.FindStringSubmatch(part)
		v, err := Parse(m[2])
		if err != nil {
			return nil, err
		}
		out = append(out, Constraint{Op: Op(m[1]), Version: v})
	}
	return out, nil
}

// Parse parses s as a Version.
func Parse(s string) (Version, error) {
	m := versionPattern.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("%w: %q is not a version such as 1.6.0", ErrSyntax, s)
	}
	segs := strings.Split(m[1], ".")
	if len(segs) > MaxSegments {
		return Version{}, fmt.Errorf("%w: %q has more than %d numeric segments",
			ErrSyntax, s, MaxSegments)
	}
	v := Version{Segments: make([]int64, len(segs)), Prerelease: m[2], Metadata: m[3]}
	for i, seg := range segs {
		n, err := strconv.ParseInt(seg, 10, 64)
		if err != nil {
			return Version{}, fmt.Errorf("%w: %q has a segment too large to compare", ErrSyntax, s)
		}
		v.Segments[i] = n
	}
	return v, nil
}

// segment is v's i-th numeric segment, 0 past the ones it names, so 1.5
// compares as 1.5.0.
func (v Version) segment(i int) int64 {
	if i < len(v.Segments) {
		return v.Segments[i]
	}
	return 0
}

// Compare orders v and w as -1, 0 or +1: by their numeric segments, then a
// prerelease before its release, then prereleases identifier by
// identifier, numeric ones by value and below any other. Build metadata
// does not order.
func (v Version) Compare(w Version) int {
	for i := range MaxSegments {
		if c := cmp.Compare(v.segment(i), w.segment(i)); c != 0 {
			return c
		}
	}
	switch {
	case v.Prerelease == w.Prerelease:
		return 0
	case v.Prerelease == "":
		return 1
	case w.Prerelease == "":
		return -1
	}
	a, b := strings.Split(v.Prerelease, "."), strings.Split(w.Prerelease, ".")
	for i := range min(len(a), len(b)) {
		if c := compareIdentifier(a[i], b[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a), len(b))
}

func compareIdentifier(a, b string) int {
	x, errA := strconv.ParseUint(a, 10, 64)
	y, errB := strconv.ParseUint(b, 10, 64)
	switch {
	case errA == nil && errB == nil:
		return cmp.Compare(x, y)
	case errA == nil:
		return -1
	case errB == nil:
		return 1
	}
	return strings.Compare(a, b)
}

// String is v as it parsed, less its build metadata.
func (v Version) String() string {
	segs := make([]string, len(v.Segments))
	for i, s := range v.Segments {
		segs[i] = strconv.FormatInt(s, 10)
	}
	out := strings.Join(segs, ".")
	if v.Prerelease != "" {
		out += "-" + v.Prerelease
	}
	return out
}

// Allows reports whether v meets every constraint, as Terraform decides
// its required_version: a prerelease meets a constraint only when the
// constraint names a prerelease of the same release, so ">= 1.5" never
// picks 1.6.0-beta1.
func (cs Constraints) Allows(v Version) bool {
	for _, c := range cs {
		if !c.allows(v) {
			return false
		}
	}
	return true
}

func (c Constraint) allows(v Version) bool {
	if v.Prerelease != "" && (c.Version.Prerelease == "" ||
		Version{Segments: v.Segments}.Compare(Version{Segments: c.Version.Segments}) != 0) {
		return false
	}
	n := v.Compare(c.Version)
	switch c.Op {
	case NotEqual:
		return n != 0
	case Greater:
		return n > 0
	case GreaterEqual:
		return n >= 0
	case Less:
		return n < 0
	case LessEqual:
		return n <= 0
	case Pessimistic:
		// The segments before the last one the constraint names stay
		// fixed; that one and those after it may grow.
		for i := range len(c.Version.Segments) - 1 {
			if v.segment(i) != c.Version.Segments[i] {
				return false
			}
		}
		return n >= 0
	default: // Exact and Equal
		return n == 0
	}
}
