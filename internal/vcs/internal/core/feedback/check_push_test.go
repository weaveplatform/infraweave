package feedback

import (
	"strings"
	"testing"
	"time"

	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/vcs/internal/core/port"
)

func TestUnitAggregateLatestIgnoredPushDecision(t *testing.T) {
	for _, failed := range []bool{false, true} {
		s, f := newAggregateWorld(t)
		run := f.run
		run.State = kernel.RunStateFinished
		run.Type = kernel.RunTypeProposed
		f.rows = append(f.rows, run)
		b := f.kept
		b.Run = kernel.ULID{}
		b.Type = kernel.RunTypeProposed
		b.CreatedAt = run.CreatedAt.Add(time.Second)
		b.Push = &PushCheck{Failed: failed, Message: "public rule", Policies: "Guard"}
		f.caps = []CheckBinding{b}
		report, _, _, found, err := s.aggregateReport(t.Context(), f.kept, "infraweave/proposed")
		want := port.CheckSkipped
		if failed {
			want = port.CheckFailure
		}
		if err != nil || !found || report.State != want || !strings.Contains(report.Summary, "public rule") || strings.Contains(report.Summary, "FINISHED") {
			t.Fatalf("latest no-run decision=%+v found=%t error=%v", report, found, err)
		}
		f.rows[0].CreatedAt = b.CreatedAt.Add(time.Second)
		report, _, _, found, err = s.aggregateReport(t.Context(), f.kept, "infraweave/proposed")
		if err != nil || !found || report.State != port.CheckSuccess || strings.Contains(report.Summary, "public rule") {
			t.Fatalf("newer real run did not supersede old ignore=%+v error=%v", report, err)
		}
	}
	state, title, message := pushCheckContent(&PushCheck{})
	if state != port.CheckSkipped || title != "Push ignored" || message != "Push ignored." {
		t.Fatal("empty no-run content has no fixed default")
	}
}
