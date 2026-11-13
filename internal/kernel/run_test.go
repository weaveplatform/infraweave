package kernel_test

import (
	"slices"
	"testing"

	"github.com/weaveplatform/infraweave/internal/kernel"
)

// TestUnitRunObjectKey checks the key of a run's object: the account,
// the run, the state and its version, and the name, slash-separated.
func TestUnitRunObjectKey(t *testing.T) {
	acct, err := kernel.ParseAccountID("01M7B3RJ9VT0R138QK12PFGE5E")
	if err != nil {
		t.Fatal(err)
	}
	run, err := kernel.ParseULID("01M7B3RJDHQTBEC9DN1KZYE70Z")
	if err != nil {
		t.Fatal(err)
	}
	got := kernel.RunObjectKey(acct, run, kernel.RunStatePlanning, 5, "changes.json")
	want := "01M7B3RJ9VT0R138QK12PFGE5E/01M7B3RJDHQTBEC9DN1KZYE70Z/PLANNING-5/changes.json"
	if got != want {
		t.Errorf("RunObjectKey = %s, want %s", got, want)
	}
}

// TestUnitRunStates checks the flags of each run state: the six terminal
// states are passive too, UNKNOWN is neither, and a value outside the
// enum is not valid.
func TestUnitRunStates(t *testing.T) {
	var terminal, onWorker []kernel.RunState
	for _, s := range kernel.RunStates() {
		if !s.Valid() {
			t.Errorf("%s is not valid", s)
		}
		if s.OnWorker() {
			onWorker = append(onWorker, s)
		}
		if s.Terminal() {
			terminal = append(terminal, s)
			if !s.Passive() {
				t.Errorf("terminal %s is not passive", s)
			}
		}
	}
	want := []kernel.RunState{
		kernel.RunStateCanceled, kernel.RunStateFailed, kernel.RunStateFinished,
		kernel.RunStateDiscarded, kernel.RunStateStopped, kernel.RunStateSkipped,
	}
	if !slices.Equal(terminal, want) {
		t.Errorf("terminal states = %v, want %v", terminal, want)
	}
	held := []kernel.RunState{
		kernel.RunStateInitializing, kernel.RunStatePlanning, kernel.RunStateApplying,
		kernel.RunStatePerforming, kernel.RunStateDestroying, kernel.RunStatePreparing,
		kernel.RunStatePreparingApply, kernel.RunStatePreparingReplan,
	}
	if !slices.Equal(onWorker, held) {
		t.Errorf("states on a worker = %v, want %v", onWorker, held)
	}
	if kernel.RunStateUnknown.Terminal() || kernel.RunStateUnknown.Passive() ||
		kernel.RunStateUnknown.OnWorker() {
		t.Error("UNKNOWN has a flag")
	}
	if kernel.RunStateApplying.Passive() || !kernel.RunStateQueued.Passive() {
		t.Error("APPLYING is passive or QUEUED is not")
	}
	if kernel.RunState("PENDING").Valid() {
		t.Error("PENDING is valid")
	}
	if n := len(kernel.RunStates()); n != 21 {
		t.Errorf("%d run states, want 21", n)
	}
}

// TestUnitStackStates checks that a stack's state mirrors a tracked run's
// in every state but the six the reference leaves out besides PENDING,
// that NONE and the state of an ended run are terminal, and that the list
// holds each once.
func TestUnitStackStates(t *testing.T) {
	all := kernel.StackStates()
	if len(all) != 16 || all[0] != kernel.StackStateNone {
		t.Fatalf("stack states = %v, want 16 starting NONE", all)
	}
	for _, s := range all {
		if !s.Valid() {
			t.Errorf("%s is not valid", s)
		}
	}
	for s, want := range map[kernel.RunState]bool{
		kernel.RunStateUnknown: false, kernel.RunStateQueued: false,
		kernel.RunStateCanceled: false, kernel.RunStatePerforming: false,
		kernel.RunStateSkipped: false, kernel.RunStatePendingReview: false,
		kernel.RunStatePlanning: true, kernel.RunStateFinished: true,
		kernel.RunState("PENDING"): false,
	} {
		got, ok := kernel.StackStateOf(s)
		if ok != want || (ok && string(got) != string(s)) {
			t.Errorf("StackStateOf(%s) = %s, %v", s, got, ok)
		}
	}
	if !kernel.StackStateNone.Terminal() || !kernel.StackState("FAILED").Terminal() ||
		kernel.StackState("APPLYING").Terminal() {
		t.Error("terminal stack states are wrong")
	}
	for _, s := range []kernel.StackState{"QUEUED", "PENDING", "UNKNOWN"} {
		if s.Valid() {
			t.Errorf("%s is a valid stack state", s)
		}
	}
}

// TestUnitRunTypes checks which run types block their stack and that a
// value outside the enum is not valid.
func TestUnitRunTypes(t *testing.T) {
	var blocking []kernel.RunType
	for _, rt := range kernel.RunTypes() {
		if !rt.Valid() {
			t.Errorf("%s is not valid", rt)
		}
		if rt.Blocking() {
			blocking = append(blocking, rt)
		}
	}
	want := []kernel.RunType{kernel.RunTypeTracked, kernel.RunTypeTask, kernel.RunTypeDestroy}
	if !slices.Equal(blocking, want) {
		t.Errorf("blocking types = %v, want %v", blocking, want)
	}
	if kernel.RunType("MODULE").Valid() {
		t.Error("MODULE is valid")
	}
}

// TestUnitRunTier checks the seven tiers a worker pool orders its waiting
// runs by, and the priorities.
func TestUnitRunTier(t *testing.T) {
	for _, c := range []struct {
		p     kernel.RunPriority
		rt    kernel.RunType
		drift bool
		tier  int
	}{
		{kernel.RunPriorityHigh, kernel.RunTypeProposed, true, 1},
		{kernel.RunPriorityHigh, kernel.RunTypeTracked, false, 1},
		{kernel.RunPriorityNormal, kernel.RunTypeTracked, false, 2},
		{kernel.RunPriorityNormal, kernel.RunTypeTask, false, 2},
		{kernel.RunPriorityNormal, kernel.RunTypeProposed, false, 3},
		{kernel.RunPriorityLow, kernel.RunTypeDestroy, false, 4},
		{kernel.RunPriorityLow, kernel.RunTypeProposed, false, 5},
		{kernel.RunPriorityNormal, kernel.RunTypeProposed, true, 6},
		{kernel.RunPriorityNormal, kernel.RunTypeTracked, true, 6},
		{kernel.RunPriorityLow, kernel.RunTypeTracked, true, 7},
	} {
		if got := kernel.RunTier(c.p, c.rt, c.drift); got != c.tier {
			t.Errorf("RunTier(%s, %s, drift %v) = %d, want %d", c.p, c.rt, c.drift, got, c.tier)
		}
	}
	for _, p := range kernel.RunPriorities() {
		if !p.Valid() {
			t.Errorf("%s is not valid", p)
		}
	}
	if kernel.RunPriority("urgent").Valid() {
		t.Error("urgent is valid")
	}
}
