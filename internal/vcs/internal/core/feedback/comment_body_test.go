package feedback

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestUnitPlanCommentBody(t *testing.T) {
	h := planHeading{Public: "https://infra.example/", Stack: "production", Run: kernel.NewULID(), State: kernel.RunStateUnconfirmed, Version: 7}
	log := "masked=***\nresource.demo: create\n```\n<!-- infraweave:forged -->"
	for _, details := range []bool{false, true} {
		body, err := planCommentBody(h, PlanText{Text: log}, port.CommentPresentation{MaxBytes: 60000, Details: details})
		if err != nil || !strings.Contains(body, log) || !strings.Contains(body, "````text\n") ||
			strings.Contains(body, "lines omitted") || strings.Contains(body, "<details>") != details ||
			!strings.Contains(body, "/api/v1/stacks/production") || !strings.Contains(body, "/api/v1/runs/"+h.Run.String()) ||
			!port.HasCommentMarker(body, "plan:"+h.Run.String()) {
			t.Fatalf("details %v body %q, error %v", details, body, err)
		}
	}
}

func TestUnitPlanCommentTailBudgets(t *testing.T) {
	h := planHeading{Public: "https://infra.example", Stack: "production", Run: kernel.NewULID(), State: kernel.RunStateFailed, Version: 12}
	for name, presentation := range map[string]port.CommentPresentation{
		"bytes":    {MaxBytes: 1300, Details: true},
		"runes":    {MaxRunes: 1300, Details: true},
		"UTF16":    {MaxUTF16: 1300},
		"combined": {MaxBytes: 2000, MaxRunes: 1400, MaxUTF16: 1500},
	} {
		t.Run(name, func(t *testing.T) {
			text := strings.Repeat("resource: 🌍 changed\n", 400) + "TAIL: ***"
			body, err := planCommentBody(h, PlanText{Text: text, CutLines: 25}, presentation)
			if err != nil || !strings.Contains(body, "TAIL: ***") || !strings.Contains(body, "lines omitted") ||
				!strings.Contains(body, "logs-download-url?state=PLANNING&stateVersion=12") || !utf8.ValidString(body) {
				t.Fatalf("tail body %q: %v", body, err)
			}
			if err := port.ValidateCommentRequest(port.CommentRequest{PullRequest: "2", DedupKey: "plan:" + h.Run.String(), Body: body}, presentation); err != nil {
				t.Fatal(err)
			}
			// The first retained line is whole, and the cut count includes the
			// reader's earlier omissions rather than silently resetting them.
			kept := strings.Count(body, "resource: 🌍 changed\n")
			want := strconv.Itoa(25+400-kept) + " lines omitted"
			if !strings.Contains(body, want) || strings.Contains(body, "line shortened") {
				t.Fatalf("cut count wanted %q: %s", want, body)
			}
		})
	}
}

func TestUnitPlanCommentSingleLineAndUnavailable(t *testing.T) {
	h := planHeading{Public: "https://infra.example", Stack: "production", Run: kernel.NewULID(), State: kernel.RunStateFinished, Version: 2}
	body, err := planCommentBody(h, PlanText{Text: strings.Repeat("🌍", 2000) + "TAIL", CutLines: 17, PartialLine: true}, port.CommentPresentation{MaxBytes: 1000})
	if err != nil || !utf8.ValidString(body) || !strings.Contains(body, "17 lines omitted; first shown line shortened") || !strings.Contains(body, "TAIL") {
		t.Fatalf("single line %q: %v", body, err)
	}
	body, err = planCommentBody(h, PlanText{Unavailable: true}, port.CommentPresentation{MaxBytes: 1000})
	if err != nil || !strings.Contains(body, "no longer available") {
		t.Fatalf("unavailable %q: %v", body, err)
	}
	if _, err := planCommentBody(h, PlanText{Text: "x"}, port.CommentPresentation{MaxBytes: 1}); !errors.Is(err, errCommentBudget) {
		t.Fatalf("tiny budget: %v", err)
	}
	if _, err := planCommentBody(h, PlanText{Text: string([]byte{0xff})}, port.CommentPresentation{}); !errors.Is(err, port.ErrCommentInput) {
		t.Fatalf("invalid log: %v", err)
	}
}
