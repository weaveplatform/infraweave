package federation

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// ulid is s, a valid ULID.
func ulid(s string) kernel.ULID {
	id, err := kernel.ParseULID(s)
	if err != nil {
		panic(err)
	}
	return id
}

// issue is a tracked run of stack infra in prod, below legacy, of the
// account acme, planning, with fixed IDs and time.
func issue() Issue {
	return Issue{
		Issuer:     "https://infraweave.example.com",
		Account:    "acme",
		Spaces:     []string{"acme", "legacy", "prod-01KAPROD000000000000000000"},
		CallerType: CallerStack,
		CallerID:   "infra",
		RunType:    kernel.RunTypeTracked,
		Run:        ulid("01KAXRN0000000000000000001"),
		Phase:      kernel.RunStatePreparing,
		ID:         ulid("01KAJT10000000000000000002"),
		Now:        time.Date(2026, 11, 24, 12, 0, 0, 999, time.UTC),
		TTL:        time.Hour,
	}
}

// TestUnitFederationTokenClaims checks every claim of a stack's planning
// token with the default audience and subject, a module's testing token
// with a custom audience and template, and that the claim set names
// exactly Claims.
func TestUnitFederationTokenClaims(t *testing.T) {
	stack, err := NewToken(issue())
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "token_stack.json", stack)
	if got := stack.Expires(); !got.Equal(time.Date(2026, 11, 24, 13, 0, 0, 0, time.UTC)) {
		t.Errorf("expires %v", got)
	}

	in := issue()
	in.Audience = "sts.example.net/"
	in.Template = "{spacePath}|{callerType}:{callerId}|{runId}|{runType}|{scope}"
	in.CallerType, in.CallerID = CallerModule, "network"
	in.RunType, in.Phase = kernel.RunTypeTesting, kernel.RunStatePreparing
	in.TTL = 15 * time.Minute
	module, err := NewToken(in)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "token_module.json", module)

	doc, err := json.Marshal(stack)
	if err != nil {
		t.Fatal(err)
	}
	var members map[string]any
	if err := json.Unmarshal(doc, &members); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	slices.Sort(names)
	want := slices.Sorted(slices.Values(Claims))
	if !slices.Equal(names, want) {
		t.Errorf("claims %v, want %v", names, want)
	}
}

// TestUnitFederationTokenAudience checks the audience of each account is
// the prefix and its slug, the issuer's host when no prefix is set, so
// two accounts of one installation never share one.
func TestUnitFederationTokenAudience(t *testing.T) {
	for _, tc := range []struct{ prefix, issuer, account, want string }{
		{"", "https://infraweave.example.com", "acme", "infraweave.example.com/acme"},
		{"", "https://infraweave.example.com/", "acme-dev", "infraweave.example.com/acme-dev"},
		{"", "http://127.0.0.1:8080/iw", "acme", "127.0.0.1:8080/acme"},
		{"sts.example.net", "https://infraweave.example.com", "acme", "sts.example.net/acme"},
		{"sts.example.net/", "https://infraweave.example.com", "globex", "sts.example.net/globex"},
		{"", "::", "acme", "/acme"},
	} {
		if got := Audience(tc.prefix, tc.issuer, tc.account); got != tc.want {
			t.Errorf("Audience(%q, %q, %q) = %q, want %q", tc.prefix, tc.issuer, tc.account, got, tc.want)
		}
	}
}

// TestUnitFederationScope is the scope table for every run type in
// every phase a handover starts: a proposed run's read; a tracked run's
// read while planning and write from PREPARING_APPLY, or write throughout
// on a stack that deploys automatically; and write throughout for a task,
// a destroy run and a testing run. A parse run gets no token.
//
// ref: report 12 section 6.4
func TestUnitFederationScope(t *testing.T) {
	phases := []kernel.RunState{
		kernel.RunStatePreparing, kernel.RunStatePreparingReplan, kernel.RunStatePreparingApply,
	}
	var b strings.Builder
	for _, rt := range kernel.RunTypes() {
		for _, autodeploy := range []bool{false, true} {
			for _, phase := range phases {
				scope, err := Scope(rt, phase, autodeploy)
				if err != nil {
					if !errors.Is(err, ErrNoToken) {
						t.Errorf("%s: %v", rt, err)
					}
					scope = "none"
				}
				b.WriteString(string(rt) + " autodeploy=" + map[bool]string{false: "off", true: "on"}[autodeploy] +
					" " + string(phase) + ": " + scope + "\n")
			}
		}
	}
	golden(t, "scope.json", strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n"))
	if s, err := Scope(kernel.RunTypeTracked, kernel.RunStateApplying, false); err != nil || s != ScopeWrite {
		t.Errorf("a tracked run applying = %q, %v", s, err)
	}
}

// TestUnitFederationTokenRefused checks that a request missing a part, a
// caller of another type, a parse run and a subject over its bound are
// each refused.
func TestUnitFederationTokenRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(*Issue)
		want   error
	}{
		"parse run":   {func(in *Issue) { in.RunType = kernel.RunTypeParse }, ErrNoToken},
		"caller type": {func(in *Issue) { in.CallerType = "worker" }, ErrNoToken},
		"no caller":   {func(in *Issue) { in.CallerID = "" }, ErrNoToken},
		"no account":  {func(in *Issue) { in.Account = "" }, ErrNoToken},
		"no issuer":   {func(in *Issue) { in.Issuer = "" }, ErrNoToken},
		"no space":    {func(in *Issue) { in.Spaces = nil }, ErrNoToken},
		"no run":      {func(in *Issue) { in.Run = kernel.ULID{} }, ErrNoToken},
		"no jti":      {func(in *Issue) { in.ID = kernel.ULID{} }, ErrNoToken},
		"no lifetime": {func(in *Issue) { in.TTL = 0 }, ErrNoToken},
		"template":    {func(in *Issue) { in.Template = "{nope}" }, ErrInvalid},
		"long subject": {func(in *Issue) {
			in.Template = strings.Repeat("{spacePath}", 90)
		}, ErrInvalid},
	} {
		in := issue()
		tc.change(&in)
		if _, err := NewToken(in); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
}
