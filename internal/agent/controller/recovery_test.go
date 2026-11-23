package controller

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"github.com/weaveplatform/infraweave/internal/agent/agenttest"
	"github.com/weaveplatform/infraweave/internal/agent/executor"
	"github.com/weaveplatform/infraweave/internal/agent/executor/kubernetes"
	"github.com/weaveplatform/infraweave/internal/agent/launcher"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// runOn assigns a run to the one slot of the build pool, of the remote
// pool, under c, and waits until its runner was handed off; it answers
// the run and its Pod's name.
func (e *env) runOn(c *Controller, pool string) (string, string) {
	e.t.Helper()
	e.until(c, "build", func(wp WorkerPool, ws []Worker) bool {
		return len(ws) == 1 && wp.Status.Ready == 1 && len(e.in.pollRequests()) > 0
	})
	run := kernel.NewULID().String()
	e.in.assign(pool, wire.Assignment{
		Run: run, RunToken: agenttest.RunToken(run), State: wire.AssignmentStatePREPARING, StateVersion: 2,
		PreparingDeadline: time.Now().Add(15 * time.Minute).Unix(),
	}, wire.RunSpec{}, wire.RunSpecSecrets{ApiToken: "opened-secret"})
	e.until(c, "build", func(_ WorkerPool, ws []Worker) bool {
		a := ws[0].Status.Assignment
		return a != nil && a.HandoffAcknowledged
	})
	return run, kubernetes.PodName(run, 2)
}

// exits are the exits the installation took of run's polls.
func (e *env) exits(run string) []wire.ContainerExit {
	var out []wire.ContainerExit
	for _, r := range e.in.pollRequests() {
		if r.Exit != nil && r.Run != nil && *r.Run == run {
			out = append(out, *r.Exit)
		}
	}
	return out
}

// lead ends c's slots when the test ends, as losing the lease does.
func lead(t *testing.T, c *Controller) {
	t.Helper()
	t.Cleanup(func() { c.stopSlots(nil) })
}

// TestUnitSlotsResumeARunOnTheNextLeader checks that a replica that
// takes the lead adopts the run Pod a previous leader handed off, from
// the assignment its Worker records, and reports the run's one exit
// without creating another Pod.
func TestUnitSlotsResumeARunOnTheNextLeader(t *testing.T) {
	e := newEnv(t)
	k := newKubelet(e)
	var created atomic.Int32
	e.core.PrependReactor("create", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		created.Add(1)
		return false, nil, nil
	})
	first := e.executing(k)
	lead(t, first)
	_, remote := e.hosted("build", Existing, 1)
	run, name := e.runOn(first, remote.id)

	// The leader loses the lease: its slot ends and leaves the Pod.
	first.stopSlots(nil)
	if _, err := e.core.CoreV1().Pods(namespace).Get(t.Context(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("the Pod went with its leader: %v", err)
	}
	if a := e.workers("build")[0].Status.Assignment; a == nil || a.RunID != run || a.PodName != name || a.PreparationDeadline == nil {
		t.Fatalf("the Worker records no assignment: %+v", a)
	}

	next := e.executing(k)
	lead(t, next)
	e.until(next, "build", func(wp WorkerPool, ws []Worker) bool {
		return wp.Status.Busy == 1 && len(ws) == 1 && ws[0].Status.Assignment != nil
	})
	k.set(t.Context(), namespace, name, func(p *corev1.Pod) {
		p.Status.Phase = corev1.PodSucceeded
		p.Status.ContainerStatuses[0].State = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}}
	})
	e.until(next, "build", func(wp WorkerPool, ws []Worker) bool {
		return len(e.exits(run)) > 0 && wp.Status.Busy == 0 && ws[0].Status.Assignment == nil
	})
	if exits := e.exits(run); !slices.Equal(exits, []wire.ContainerExit{{Code: 0, Reason: wire.Exited}}) {
		t.Fatalf("exits %+v", exits)
	}
	if n := created.Load(); n != 1 {
		t.Fatalf("Pods created: %d", n)
	}
	if _, err := e.core.CoreV1().Pods(namespace).Get(t.Context(), name, metav1.GetOptions{}); err == nil {
		t.Fatal("the ended run's Pod remains")
	}
}

// TestUnitSlotsKillAgainOnTheNextLeader checks that a kill whose Pod
// deletion failed is recorded on the Worker, and that the next leader
// deletes the Pod and reports the run killed.
func TestUnitSlotsKillAgainOnTheNextLeader(t *testing.T) {
	e := newEnv(t)
	k := newKubelet(e)
	var refuse atomic.Bool
	e.core.PrependReactor("delete", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		if refuse.Load() {
			return true, nil, errors.New("the API server is unavailable")
		}
		return false, nil, nil
	})
	first := e.executing(k)
	lead(t, first)
	_, remote := e.hosted("build", Existing, 1)
	run, name := e.runOn(first, remote.id)

	refuse.Store(true)
	e.in.signal(remote.id, wire.Kill)
	e.until(first, "build", func(_ WorkerPool, ws []Worker) bool {
		a := ws[0].Status.Assignment
		return a != nil && a.KillRequested
	})
	first.stopSlots(nil)
	refuse.Store(false)

	next := e.executing(k)
	lead(t, next)
	e.until(next, "build", func(_ WorkerPool, ws []Worker) bool {
		return len(e.exits(run)) > 0 && len(ws) == 1 && ws[0].Status.Assignment == nil
	})
	if exits := e.exits(run); exits[0] != (wire.ContainerExit{Code: 137, Reason: wire.Killed}) {
		t.Fatalf("exits %+v", exits)
	}
	if _, err := e.core.CoreV1().Pods(namespace).Get(t.Context(), name, metav1.GetOptions{}); err == nil {
		t.Fatal("the killed run's Pod remains")
	}
}

// TestUnitSlotsRetireAResumedRun checks that a Worker retired while its
// run executes on no launcher of this replica gets one, which resumes
// the run to its end before the Worker goes.
func TestUnitSlotsRetireAResumedRun(t *testing.T) {
	e := newEnv(t)
	k := newKubelet(e)
	first := e.executing(k)
	lead(t, first)
	_, remote := e.hosted("build", Existing, 1)
	run, name := e.runOn(first, remote.id)
	first.stopSlots(nil)

	next := e.executing(k)
	lead(t, next)
	e.change("build", func(wp *WorkerPool) { wp.Spec.Capacity = 0 })
	next.Pass(t.Context())
	ws := e.workers("build")
	if len(ws) != 1 {
		t.Fatalf("a busy slot was retired: %d", len(ws))
	}
	expect(t, ws[0].Status.Conditions, ConditionTerminating, metav1.ConditionFalse, ReasonCleanupBlocked)
	k.set(t.Context(), namespace, name, func(p *corev1.Pod) {
		p.Status.Phase = corev1.PodSucceeded
		p.Status.ContainerStatuses[0].State = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 3}}
	})
	e.until(next, "build", func(_ WorkerPool, ws []Worker) bool { return len(ws) == 0 })
	if exits := e.exits(run); len(exits) == 0 || exits[0].Code != 3 {
		t.Fatalf("exits %+v", exits)
	}
}

// TestUnitSlotsRecordDrainAndConnection checks that a Worker's
// conditions show whether the installation drains it and answers its
// polls, and that a drained worker does not count as ready.
func TestUnitSlotsRecordDrainAndConnection(t *testing.T) {
	e := newEnv(t)
	c := e.executing(newKubelet(e))
	lead(t, c)
	_, remote := e.hosted("build", Existing, 1)
	e.until(c, "build", func(wp WorkerPool, ws []Worker) bool {
		return wp.Status.Ready == 1 && len(ws) == 1 && hasCondition(ws[0], ConditionConnected, metav1.ConditionTrue)
	})
	expect(t, e.workers("build")[0].Status.Conditions, ConditionDrained, metav1.ConditionFalse, ReasonAccepting)

	e.in.drain(remote.id, true)
	e.until(c, "build", func(wp WorkerPool, ws []Worker) bool {
		return wp.Status.Ready == 0 && len(ws) == 1 && hasCondition(ws[0], ConditionDrained, metav1.ConditionTrue)
	})
	e.in.drain(remote.id, false)
	e.until(c, "build", func(wp WorkerPool, ws []Worker) bool {
		return wp.Status.Ready == 1 && hasCondition(ws[0], ConditionDrained, metav1.ConditionFalse)
	})

	e.in.refusing("poll", 503)
	e.until(c, "build", func(_ WorkerPool, ws []Worker) bool {
		return hasCondition(ws[0], ConditionConnected, metav1.ConditionFalse)
	})
	expect(t, e.workers("build")[0].Status.Conditions, ConditionConnected, metav1.ConditionFalse, ReasonServerUnreachable)
}

func hasCondition(w Worker, typ string, status metav1.ConditionStatus) bool {
	for _, c := range w.Status.Conditions {
		if c.Type == typ {
			return c.Status == status
		}
	}
	return false
}

// TestUnitSlotStoreRoundTrips checks that a slot saves and loads its
// launcher's state through its Worker's status, replacing every field of
// an assignment, and neither reads nor writes a Worker that replaced its
// own.
func TestUnitSlotStoreRoundTrips(t *testing.T) {
	e := newEnv(t)
	c := e.controller()
	e.hosted("build", Existing, 1)
	e.pass(c)
	w := e.workers("build")[0]
	s := &slot{c: c, worker: w.ObjectMeta, runTimeout: time.Hour}
	ctx := t.Context()
	empty, err := s.Load(ctx)
	if err != nil || empty.Run != "" || empty.Worker != w.Status.WorkerID {
		t.Fatalf("load %+v %v", empty, err)
	}
	at := time.Unix(1_900_000_000, 0)
	st := launcher.State{
		Pool: empty.Pool, Worker: empty.Worker, Run: "run-1", Assigned: wire.AssignmentStatePREPARINGAPPLY, StateVersion: 3,
		PreparingDeadline: at, Started: at.Add(time.Minute), Kill: true, Exit: &wire.ContainerExit{Code: 137, Reason: wire.Killed},
	}
	s.observe(kubernetes.Observation{Run: "run-1", Pod: kubernetes.PodName("run-1", 3), UID: "pod-uid", Phase: kubernetes.PhaseAcknowledged})
	if err := s.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	a := e.workers("build")[0].Status.Assignment
	if a == nil || !a.KillRequested || a.ExitReason != "killed" || a.PodUID != "pod-uid" || !a.RunDeadline.Time.Equal(at.Add(time.Hour+time.Minute)) {
		t.Fatalf("assignment %+v", a)
	}
	again := &slot{c: c, worker: w.ObjectMeta, runTimeout: time.Hour}
	got, err := again.Load(ctx)
	if err != nil || !got.PreparingDeadline.Equal(st.PreparingDeadline) || !got.Started.Equal(st.Started) {
		t.Fatalf("load %+v %v", got, err)
	}
	got.PreparingDeadline, got.Started = st.PreparingDeadline, st.Started
	if !reflect.DeepEqual(got, st) {
		t.Fatalf("load %+v, want %+v", got, st)
	}
	// A later state without an exit or a kill leaves neither behind.
	st.Exit, st.Kill, st.Started = nil, false, time.Time{}
	if err := again.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	if a := e.workers("build")[0].Status.Assignment; a.KillRequested || a.ExitCode != nil || a.ExitReason != "" || a.StartedAt != nil || a.RunDeadline != nil {
		t.Fatalf("a field survived: %+v", a)
	}
	if err := again.Forget(ctx); err != nil {
		t.Fatal(err)
	}
	if w := e.workers("build")[0]; w.Status.Assignment != nil || w.Status.WorkerID == "" {
		t.Fatalf("forgotten: %+v", w.Status)
	}

	replaced := w.ObjectMeta
	replaced.UID = "another"
	stale := &slot{c: c, worker: replaced}
	if _, err := stale.Load(ctx); !errors.Is(err, errReplaced) {
		t.Fatalf("load of a replaced Worker: %v", err)
	}
	// The API server refuses a patch whose UID differs from the object's,
	// which the fake does not; the patch must carry it.
	var patched []byte
	e.dyn.PrependReactor("patch", "workers", func(a k8stesting.Action) (bool, runtime.Object, error) {
		patched = a.(k8stesting.PatchAction).GetPatch()
		return true, nil, errors.New("uid mismatch")
	})
	if err := stale.Save(ctx, st); err == nil || !strings.Contains(string(patched), `"uid":"another"`) {
		t.Fatalf("save into a replaced Worker: %v, patch %s", err, patched)
	}
	gone := w.ObjectMeta
	gone.Name = "absent"
	if _, err := (&slot{c: c, worker: gone}).Load(ctx); err == nil || !strings.Contains(err.Error(), "absent") {
		t.Fatalf("load of an absent Worker: %v", err)
	}
}

// TestUnitSlotRecordsAnAcknowledgedHandoffWithItsStart checks that a
// Worker's status acknowledges a run's handoff only together with the
// run's start and run deadline, which the launcher records after the
// executor observes the acknowledgement, and keeps the acknowledgement
// with the run's exit, which clears its start.
func TestUnitSlotRecordsAnAcknowledgedHandoffWithItsStart(t *testing.T) {
	e := newEnv(t)
	c := e.controller()
	e.hosted("build", Existing, 1)
	e.pass(c)
	w := e.workers("build")[0]
	s := &slot{c: c, worker: w.ObjectMeta, runTimeout: time.Hour}
	ctx := t.Context()
	st, err := s.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	st.Run, st.Assigned, st.StateVersion = "run-1", wire.AssignmentStatePREPARING, 2
	if err := s.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	pod := kubernetes.PodName("run-1", 2)
	s.observe(kubernetes.Observation{Run: "run-1", Pod: pod, UID: "pod-uid", Phase: kubernetes.PhaseStaged})
	s.observe(kubernetes.Observation{Run: "run-1", Pod: pod, UID: "pod-uid", Phase: kubernetes.PhaseAcknowledged})
	a := e.workers("build")[0].Status.Assignment
	if a == nil || a.HandoffAcknowledged || a.HandoffPhase != string(kubernetes.PhaseStaged) || a.PodUID != "pod-uid" || a.StartedAt != nil {
		t.Fatalf("an acknowledgement recorded before its start: %+v", a)
	}

	st.Started = time.Unix(1_900_000_000, 0)
	if err := s.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	a = e.workers("build")[0].Status.Assignment
	if !a.HandoffAcknowledged || a.HandoffPhase != string(kubernetes.PhaseAcknowledged) || a.StartedAt == nil ||
		!a.RunDeadline.Time.Equal(st.Started.Add(time.Hour)) {
		t.Fatalf("the acknowledgement without its start: %+v", a)
	}

	st.Started, st.Exit = time.Time{}, &wire.ContainerExit{Code: executor.MissingCode, Reason: wire.Missing}
	if err := s.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	a = e.workers("build")[0].Status.Assignment
	if !a.HandoffAcknowledged || a.HandoffPhase != string(kubernetes.PhaseAcknowledged) || a.StartedAt != nil || a.ExitReason != string(wire.Missing) {
		t.Fatalf("an ended run's acknowledgement: %+v", a)
	}
}

// TestUnitAssignmentFieldsNameEveryField checks that the merge patch of
// an assignment names every field of AssignmentStatus, so none survives
// from the assignment it replaces.
func TestUnitAssignmentFieldsNameEveryField(t *testing.T) {
	ty := reflect.TypeFor[AssignmentStatus]()
	names := make([]string, 0, ty.NumField())
	for i := range ty.NumField() {
		names = append(names, strings.Split(ty.Field(i).Tag.Get("json"), ",")[0])
	}
	slices.Sort(names)
	got := slices.Sorted(slices.Values(assignmentFields))
	if !slices.Equal(names, got) {
		t.Fatalf("fields %v, patched %v", names, got)
	}
	if assignmentPatch(nil) != nil {
		t.Fatal("an idle slot's patch keeps an assignment")
	}
}
