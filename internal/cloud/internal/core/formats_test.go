package core

import (
	"errors"
	"strings"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

func TestUnitExternalIDs(t *testing.T) {
	id := mustULID(t, "01JC0000000000000000000000")
	if got, want := ExternalID("acme", id, "web", Read), "acme@01JC0000000000000000000000@web@read"; got != want {
		t.Fatalf("external ID = %q, want %q", got, want)
	}
	if got, want := ExternalID("acme", id, "web", Write), "acme@01JC0000000000000000000000@web@write"; got != want {
		t.Fatalf("external ID = %q, want %q", got, want)
	}
	if got := LegacyExternalID("acme", "web"); got != "acme@web" {
		t.Fatalf("legacy external ID = %q", got)
	}
}

func TestUnitSessionName(t *testing.T) {
	run := mustULID(t, "01JC0000000000000000000001")
	if got, want := SessionName(run, "web", "acme"), "01JC0000000000000000000001@web@acme"; got != want {
		t.Fatalf("session name = %q, want %q", got, want)
	}
	long := SessionName(run, strings.Repeat("s", 40), "acme")
	if len(long) != MaxSessionName || !strings.HasPrefix(long, run.String()+"@sss") {
		t.Fatalf("session name = %q (%d)", long, len(long))
	}
}

func TestUnitPolicyStatement(t *testing.T) {
	one := `{
  "Effect": "Allow",
  "Principal": {
    "AWS": "111111111111"
  },
  "Action": "sts:AssumeRole",
  "Condition": {
    "StringEquals": {
      "sts:ExternalId": "acme@web"
    }
  }
}`
	if got := PolicyStatement("111111111111", "acme@web"); got != one {
		t.Fatalf("statement = %s", got)
	}
	two := PolicyStatement("111111111111", "a@r", "a@w")
	if !strings.Contains(two, `"sts:ExternalId": [
        "a@r",
        "a@w"
      ]`) {
		t.Fatalf("statement = %s", two)
	}
}

func TestUnitErrors(t *testing.T) {
	cause := errors.New("throttled")
	for err, want := range map[error]string{
		&NameTaken{Name: "prod"}:            `cloud: an AWS integration named "prod" exists`,
		&Conflict{Detail: "taken"}:          "cloud: taken",
		&Missing{Kind: "stack", ID: "web"}:  `cloud: no stack "web"`,
		&Denied{Action: "SPACE_ADMIN"}:      "you may not perform SPACE_ADMIN here",
		&Upstream{Code: "x", Err: cause}:    "cloud: AWS STS: throttled",
		&Refused{Message: "not authorized"}: "cloud: AWS STS denied the assumption: not authorized",
	} {
		if err.Error() != want {
			t.Errorf("error = %q, want %q", err.Error(), want)
		}
	}
	if !errors.Is(&Upstream{Err: cause}, cause) {
		t.Fatal("an upstream error hides its cause")
	}
}

func TestUnitAttachmentIDs(t *testing.T) {
	i, st := kernel.NewULID(), kernel.NewULID()
	stored := Attachment{ID: kernel.NewULID(), Integration: i, Stack: st}
	if stored.APIID() != stored.ID.String() {
		t.Fatalf("stored ID = %q", stored.APIID())
	}
	auto := Attachment{Integration: i, Stack: st, Auto: true}
	if got, want := auto.APIID(), "auto-"+i.String()+"-"+st.String(); got != want {
		t.Fatalf("automatic ID = %q, want %q", got, want)
	}
}

func TestUnitActor(t *testing.T) {
	run, key := kernel.NewULID(), kernel.NewULID()
	for _, tc := range []struct {
		p    kernel.Principal
		want kernel.AuditActor
	}{
		{kernel.Principal{Kind: kernel.PrincipalRun, ActorID: run}, kernel.AuditActor{Username: Actor, Run: run}},
		{kernel.Principal{Kind: kernel.PrincipalAPIKey, ActorID: key, Login: "a"}, kernel.AuditActor{Username: "a"}},
		{kernel.Principal{Kind: kernel.PrincipalAPIKey, ActorID: key}, kernel.AuditActor{Username: key.String()}},
	} {
		if got := actorOf(tc.p); got != tc.want {
			t.Errorf("actor of %+v = %+v, want %+v", tc.p, got, tc.want)
		}
	}
}

func mustULID(t *testing.T, s string) kernel.ULID {
	t.Helper()
	id, err := kernel.ParseULID(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
