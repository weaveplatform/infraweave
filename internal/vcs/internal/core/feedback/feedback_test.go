package feedback

import (
	"errors"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
	"github.com/weaveplatform/infraweave/internal/platform/queue"
	runsapi "github.com/weaveplatform/infraweave/internal/runs/api"
	vcsapi "github.com/weaveplatform/infraweave/internal/vcs/api"
)

func TestUnitFeedbackFoundation(t *testing.T) {
	s := New(queue.New(nil), nil)
	kinds := s.Kinds()
	if len(kinds) != 1 || kinds[0] != runsapi.UpdatedKind {
		t.Fatalf("kinds %v", kinds)
	}
	kinds[0] = "changed"
	if s.Kinds()[0] != runsapi.UpdatedKind {
		t.Fatal("Kinds exposes shared state")
	}
	targets, err := s.Targets(t.Context(), outbox.Event{})
	if err != nil || len(targets) != 0 {
		t.Fatalf("foundation targets %v, %v", targets, err)
	}
	if err := s.Enqueue(t.Context(), vcsapi.FeedbackTarget{}); !errors.Is(err, queue.ErrNoTransaction) {
		t.Fatalf("Enqueue outside transaction = %v", err)
	}
}

func TestUnitFeedbackTargetValidation(t *testing.T) {
	good := vcsapi.FeedbackTarget{Account: kernel.NewULID(), EventID: kernel.NewULID(), Key: "run:check", Job: ReportCheck.Name, Payload: []byte(`{"run":"id"}`)}
	if !valid(good) {
		t.Fatal("valid target rejected")
	}
	for name, change := range map[string]func(*vcsapi.FeedbackTarget){
		"no account":       func(t *vcsapi.FeedbackTarget) { t.Account = kernel.ULID{} },
		"no event":         func(t *vcsapi.FeedbackTarget) { t.EventID = kernel.ULID{} },
		"no key":           func(t *vcsapi.FeedbackTarget) { t.Key = "" },
		"unknown job":      func(t *vcsapi.FeedbackTarget) { t.Job = "vcs.unimplemented" },
		"invalid document": func(t *vcsapi.FeedbackTarget) { t.Payload = []byte("{") },
	} {
		t.Run(name, func(t *testing.T) {
			target := good
			change(&target)
			if valid(target) {
				t.Fatal("invalid target accepted")
			}
		})
	}
}
