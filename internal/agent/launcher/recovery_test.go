package launcher_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/weaveplatform/infraweave/internal/agent/executor"
	"github.com/weaveplatform/infraweave/internal/agent/launcher"
	"github.com/weaveplatform/infraweave/internal/agent/wire"
)

// memoryStore keeps a launcher's state in memory, as a worker
// controller's Worker resource does, failing a load or a save on demand.
type memoryStore struct {
	mu      sync.Mutex
	st      launcher.State
	loadErr error
	saveErr error
	saves   int
}

func (m *memoryStore) Load(context.Context) (launcher.State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st, m.loadErr
}

func (m *memoryStore) Save(_ context.Context, st launcher.State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveErr != nil {
		return m.saveErr
	}
	m.st = st
	m.saves++
	return nil
}

func (m *memoryStore) Forget(ctx context.Context) error {
	return m.Save(ctx, launcher.State{})
}

func (m *memoryStore) state() launcher.State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st
}

func (m *memoryStore) set(change func(*launcher.State)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	change(&m.st)
}

func (m *memoryStore) failSaves(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saveErr = err
}

var errKillRefused = errors.New("the host refused the kill")

// TestUnitLauncherKeepsItsAssignmentInItsStore checks that the store
// holds the assignment's run, state, version and issued preparing
// deadline before the run's container starts, its start once it started,
// and nothing of the run once its exit is reported.
func TestUnitLauncherKeepsItsAssignmentInItsStore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		m := &memoryStore{}
		w.store = m
		r := w.start(t)
		run := newRun("run-1", wire.AssignmentStatePREPARING, "")
		w.queue(t, run)
		deadline := time.Unix(run.Assignment.PreparingDeadline, 0)
		st := m.state()
		if st.Run != "run-1" || st.Assigned != wire.AssignmentStatePREPARING || st.StateVersion != 4 ||
			!st.PreparingDeadline.Equal(deadline) || st.Started.IsZero() || st.Worker != "worker-1" {
			t.Fatalf("state of a started run: %+v", st)
		}
		if jobs := w.exec.Jobs(); len(jobs) != 1 || !jobs[0].PreparingDeadline.Equal(deadline) {
			t.Fatalf("jobs: %+v", jobs)
		}
		if s := r.l.Status(); s.Run != "run-1" || !s.PreparingDeadline.Equal(deadline) || s.Kill {
			t.Fatalf("status: %+v", s)
		}
		w.exec.Container("worker-1").End(ended(0, wire.Exited))
		synctest.Wait()
		if st := m.state(); st.Exit == nil || st.Run != "run-1" {
			t.Fatalf("state of an ended run: %+v", st)
		}
		w.answer(wire.PollResult{})
		if st := m.state(); st != (launcher.State{Pool: st.Pool, Worker: "worker-1"}) || st.Pool == "" {
			t.Fatalf("state once the exit was reported: %+v", st)
		}
		w.answer(wire.PollResult{Drained: new(true)})
		if !r.l.Status().Drained {
			t.Fatal("the status shows no drain")
		}
		r.stop(t)
	})
}

// TestUnitLauncherKillsAgainAfterARestart checks that a kill is saved
// before the container is killed, that a kill the host refuses is tried
// again before the next poll, and that a restarted launcher kills the
// resumed container again.
func TestUnitLauncherKillsAgainAfterARestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		m := &memoryStore{}
		w.store = m
		r := w.start(t)
		w.assign(t, "run-1", wire.AssignmentStatePREPARING, "")
		c := w.exec.Container("worker-1")
		c.FailKill(errKillRefused)
		w.answer(signals(wire.Kill))
		if c.Killed() || !m.state().Kill || !r.l.Status().Kill {
			t.Fatalf("a refused kill: killed %v, state %+v", c.Killed(), m.state())
		}
		w.answer(wire.PollResult{})
		if c.Killed() || strings.Count(w.log.String(), `msg="signal the run"`) < 2 {
			t.Fatalf("the refused kill was not tried again: %s", w.log)
		}
		r.stop(t)

		c.FailKill(nil)
		r = w.start(t)
		if !c.Killed() {
			t.Fatal("a restart left a killed run's container running")
		}
		w.wantPoll(t, false, "run-1", &wire.ContainerExit{Code: 137, Reason: wire.Killed})
		w.answer(wire.PollResult{})
		if m.state().Kill {
			t.Fatal("the kill outlived its run's reported end")
		}
		r.stop(t)
	})
}

// TestUnitLauncherRetriesARefusedKill checks that a kill the host refused
// is done before the next poll once the host takes it.
func TestUnitLauncherRetriesARefusedKill(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		r := w.start(t)
		w.assign(t, "run-1", wire.AssignmentStatePREPARING, "")
		c := w.exec.Container("worker-1")
		c.FailKill(errKillRefused)
		w.answer(signals(wire.Kill))
		c.FailKill(nil)
		w.answer(wire.PollResult{})
		if !c.Killed() {
			t.Fatal("the refused kill was not tried again")
		}
		w.wantPoll(t, false, "run-1", &wire.ContainerExit{Code: 137, Reason: wire.Killed})
		r.stop(t)
	})
}

// TestUnitLauncherReportsAKilledRunGoneDuringARestart checks that a run
// whose kill was saved, and whose container is gone after a restart,
// ends killed rather than missing.
func TestUnitLauncherReportsAKilledRunGoneDuringARestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		m := &memoryStore{}
		w.store = m
		r := w.start(t)
		w.assign(t, "run-1", wire.AssignmentStatePREPARING, "")
		c := w.exec.Container("worker-1")
		c.FailKill(errKillRefused)
		w.answer(signals(wire.Kill))
		r.stop(t)
		if err := c.Remove(t.Context()); err != nil {
			t.Fatal(err)
		}
		r = w.start(t)
		w.wantPoll(t, false, "run-1", &wire.ContainerExit{Code: 137, Reason: wire.Killed})
		if st := m.state(); st.Exit == nil || st.Exit.Reason != wire.Killed {
			t.Fatalf("the restart saved no exit: %+v", st)
		}
		r.stop(t)
	})
}

// TestUnitLauncherRemovesAnotherRunsContainerAtStart checks that a
// container of another run than the one the store holds, as one whose
// removal failed before, is killed and removed, and the store's run is
// reported missing.
func TestUnitLauncherRemovesAnotherRunsContainerAtStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		m := &memoryStore{}
		w.store = m
		r := w.start(t)
		w.assign(t, "run-1", wire.AssignmentStatePREPARING, "")
		r.stop(t)
		c := w.exec.Container("worker-1")
		m.set(func(st *launcher.State) { st.Run, st.Started = "run-2", time.Time{} })
		r = w.start(t)
		if !c.Killed() || !c.Removed() {
			t.Fatalf("another run's container: killed %v, removed %v", c.Killed(), c.Removed())
		}
		w.wantPoll(t, false, "run-2", &wire.ContainerExit{Code: executor.MissingCode, Reason: wire.Missing})
		r.stop(t)
	})
}

// TestUnitLauncherBoundsPreparationByItsDeadline checks that a
// preparation still under way when the assignment's preparing deadline
// passes fails the run, naming the deadline, and starts no container.
func TestUnitLauncherBoundsPreparationByItsDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.sources.block = true
		r := w.start(t)
		run := newRun("run-1", wire.AssignmentStatePREPARING, "")
		run.Assignment.PreparingDeadline = time.Now().Add(time.Minute).Unix()
		w.queue(t, run)
		<-time.After(2 * time.Minute)
		synctest.Wait()
		reps := w.srv.Reports()
		if len(reps) != 1 || reps[0].Report.Event != wire.Failed ||
			!strings.Contains(*reps[0].Report.Details, "the preparing deadline") ||
			len(w.exec.Jobs()) != 0 {
			t.Fatalf("reports: %+v, jobs %d", reps, len(w.exec.Jobs()))
		}
		r.stop(t)
	})
}

// TestUnitLauncherRefusesAnAssignmentItCannotRecord checks that a run
// whose assignment the store cannot save is failed unprepared.
func TestUnitLauncherRefusesAnAssignmentItCannotRecord(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		m := &memoryStore{}
		w.store = m
		r := w.start(t)
		m.failSaves(errors.New("the API server is unreachable"))
		w.assign(t, "run-1", wire.AssignmentStatePREPARING, "")
		reps := w.srv.Reports()
		if len(reps) != 1 || !strings.Contains(*reps[0].Report.Details, "record the assignment") ||
			len(w.exec.Jobs()) != 0 {
			t.Fatalf("reports: %+v, jobs %d", reps, len(w.exec.Jobs()))
		}
		m.failSaves(nil)
		r.stop(t)
	})
}

// TestUnitLauncherFailsAnUnreadableStore checks that Run fails when its
// store cannot be read, rather than registering a new worker.
func TestUnitLauncherFailsAnUnreadableStore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		w.store = &memoryStore{loadErr: errors.New("forbidden")}
		if err := w.runFor(t, time.Minute); err == nil || !strings.Contains(err.Error(), "forbidden") ||
			len(w.srv.Registrations()) != 0 {
			t.Fatalf("Run: %v, registrations %d", err, len(w.srv.Registrations()))
		}
	})
}
