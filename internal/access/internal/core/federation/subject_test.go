package federation

import (
	"errors"
	"strings"
	"testing"
)

var caller = Caller{
	SpaceID:   "prod-01KJMM56VS4W3AL9YZWVCXBX8D",
	SpacePath: "acme/prod-01KJMM56VS4W3AL9YZWVCXBX8D",
	Type:      "stack",
	ID:        "infra",
	RunID:     "01KJMM5Q4R5S6T7V8W9X0Y1Z2A",
	RunType:   "TRACKED",
	Scope:     "write",
}

// TestUnitRenderSubject checks the default and every placeholder against
// golden subjects.
func TestUnitRenderSubject(t *testing.T) {
	for _, tc := range []struct{ template, want string }{
		{"", "space:prod-01KJMM56VS4W3AL9YZWVCXBX8D:stack:infra:run_type:TRACKED:scope:write"},
		{DefaultSubjectTemplate, "space:prod-01KJMM56VS4W3AL9YZWVCXBX8D:stack:infra:run_type:TRACKED:scope:write"},
		{"{spacePath}|{runId}", "acme/prod-01KJMM56VS4W3AL9YZWVCXBX8D|01KJMM5Q4R5S6T7V8W9X0Y1Z2A"},
		{"{callerType}/{callerId}:{runType}-{scope}_x", "stack/infra:TRACKED-write_x"},
		{"fixed", "fixed"},
	} {
		got, err := RenderSubject(tc.template, caller)
		if err != nil || got != tc.want {
			t.Errorf("RenderSubject(%q) = %q, %v; want %q", tc.template, got, err, tc.want)
		}
		if err := CheckSubjectTemplate(tc.template); err != nil {
			t.Errorf("CheckSubjectTemplate(%q): %v", tc.template, err)
		}
	}
}

// TestUnitSubjectTemplateRefused checks each rule refuses with ErrInvalid
// naming what it refuses.
func TestUnitSubjectTemplateRefused(t *testing.T) {
	for _, tc := range []struct{ template, names string }{
		{strings.Repeat("a", MaxTemplate+1), "more than 1000"},
		{"space:{spaceName}", "{spaceName}"},
		{"space {spaceId}", `' '`},
		{"a\nb", `'\n'`},
		{"a&b", `'&'`},
		{"a=b", `'='`},
		{"a?b", `'?'`},
		{"a#b", `'#'`},
		{"a@b", `'@'`},
		{"a%b", `'%'`},
		{"space:{spaceId", "not closed"},
		{"space:}", "closes no placeholder"},
	} {
		err := CheckSubjectTemplate(tc.template)
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.names) {
			t.Errorf("CheckSubjectTemplate(%.20q) = %v, want ErrInvalid naming %s", tc.template, err, tc.names)
		}
		if _, err := RenderSubject(tc.template, caller); !errors.Is(err, ErrInvalid) {
			t.Errorf("RenderSubject(%.20q) = %v, want ErrInvalid", tc.template, err)
		}
	}
}

// TestUnitRenderedSubjectBound checks a template within its bound whose
// subject exceeds MaxSubject is refused when rendered.
func TestUnitRenderedSubjectBound(t *testing.T) {
	long := caller
	long.SpacePath = strings.Repeat("s", MaxSubject+1)
	if _, err := RenderSubject("{spacePath}", long); !errors.Is(err, ErrInvalid) ||
		!strings.Contains(err.Error(), "more than 2048") {
		t.Errorf("long subject: %v", err)
	}
	long.SpacePath = strings.Repeat("s", MaxSubject)
	if s, err := RenderSubject("{spacePath}", long); err != nil || len(s) != MaxSubject {
		t.Errorf("subject at the bound: %d, %v", len(s), err)
	}
}
