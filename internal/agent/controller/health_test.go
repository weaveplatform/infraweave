package controller

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

func TestUnitReadinessFollowsTheLeaseAndTheInstallation(t *testing.T) {
	e := newEnv(t)
	e.hosted("existing", Existing, 1)
	c := e.controller()
	ctx := context.Background()
	if err := c.Reachable(ctx); err == nil {
		t.Fatal("a replica the Kubernetes API never answered is ready")
	}
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- c.Run(run) }()
	deadline := time.After(20 * time.Second)
	for c.Reachable(ctx) != nil || !c.Leading() || c.Synced(ctx) != nil {
		select {
		case <-deadline:
			cancel()
			t.Fatal("the controller did not lead and reach its Lease")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := c.Connected(ctx); err != nil {
		t.Fatalf("a leader that reached its installation is unready: %v", err)
	}
	e.advance(LeaseDuration + time.Second)
	if err := c.Reachable(ctx); err == nil || !strings.Contains(err.Error(), "has not answered") {
		t.Fatalf("a replica whose Lease went unanswered for a lease = %v", err)
	}
}

func TestUnitAnUnreachableInstallationMakesTheLeaderUnready(t *testing.T) {
	for name, status := range map[string]int{"gateway": http.StatusBadGateway, "server": http.StatusInternalServerError} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.hosted("existing", Existing, 1)
			c := e.controller()
			c.leading.Store(true)
			e.in.refusing("bind", status)
			e.pass(c)
			if err := c.Connected(context.Background()); err == nil {
				t.Fatal("a leader whose installation failed is ready")
			}
			c.leading.Store(false)
			if err := c.Connected(context.Background()); err != nil {
				t.Fatalf("a standby replica is unready: %v", err)
			}
			c.leading.Store(true)
			e.in.refusing("bind", 0)
			e.pass(c)
			if err := c.Connected(context.Background()); err != nil {
				t.Fatalf("a leader that reaches its installation again is unready: %v", err)
			}
		})
	}
	t.Run("refusals", func(t *testing.T) {
		e := newEnv(t)
		e.hosted("existing", Existing, 1)
		c := e.controller()
		c.leading.Store(true)
		for _, status := range []int{http.StatusConflict, http.StatusTooManyRequests} {
			e.in.refusing("bind", status)
			e.pass(c)
			if err := c.Connected(context.Background()); err != nil {
				t.Fatalf("an installation answering %d is unreachable: %v", status, err)
			}
		}
	})
	t.Run("no answer", func(t *testing.T) {
		e := newEnv(t)
		e.hosted("existing", Existing, 1)
		c := e.controller()
		c.leading.Store(true)
		e.in.srv.Close()
		e.pass(c)
		if err := c.Connected(context.Background()); err == nil {
			t.Fatal("a leader whose installation is closed is ready")
		}
	})
}

// lockFake is a Lease lock whose calls answer err.
type lockFake struct {
	resourcelock.Interface
	err error
}

func (l lockFake) Get(context.Context) (*resourcelock.LeaderElectionRecord, []byte, error) {
	return nil, nil, l.err
}

func (l lockFake) Create(context.Context, resourcelock.LeaderElectionRecord) error { return l.err }

func (l lockFake) Update(context.Context, resourcelock.LeaderElectionRecord) error { return l.err }

func TestUnitTheLeaseLockRecordsOnlyAnswers(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		err    error
		answer [3]bool
	}{
		"answered":  {nil, [3]bool{true, true, true}},
		"absent":    {newNotFound(), [3]bool{true, false, false}},
		"no answer": {errors.New("connection refused"), [3]bool{false, false, false}},
	} {
		t.Run(name, func(t *testing.T) {
			answered := 0
			l := answeredLock{Interface: lockFake{err: tc.err}, answered: func() { answered++ }}
			_, _, _ = l.Get(ctx)
			if got := answered == 1; got != tc.answer[0] {
				t.Errorf("get answered %v", got)
			}
			answered = 0
			_ = l.Create(ctx, resourcelock.LeaderElectionRecord{})
			if got := answered == 1; got != tc.answer[1] {
				t.Errorf("create answered %v", got)
			}
			answered = 0
			_ = l.Update(ctx, resourcelock.LeaderElectionRecord{})
			if got := answered == 1; got != tc.answer[2] {
				t.Errorf("update answered %v", got)
			}
		})
	}
}

func newNotFound() error {
	return &statusError{metav1.Status{Status: metav1.StatusFailure, Reason: metav1.StatusReasonNotFound, Code: http.StatusNotFound}}
}

type statusError struct{ s metav1.Status }

func (e *statusError) Error() string         { return string(e.s.Reason) }
func (e *statusError) Status() metav1.Status { return e.s }

func TestUnitUninstallDeletesOwnedPoolsAndWaits(t *testing.T) {
	e := newEnv(t)
	owned := e.managed("owned", 1)
	label(t, e, owned.Name, map[string]string{"app.kubernetes.io/instance": "alpha"})
	c := e.controller()
	e.pass(c)
	if !hasFinalizer(e.pool("owned").ObjectMeta) {
		t.Fatal("the pool has no finalizer to wait for")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
				c.Pass(ctx)
			}
		}
	}()
	defer close(stop)
	if err := Uninstall(ctx, e.dyn, []string{namespace}, "app.kubernetes.io/instance=alpha", time.Millisecond, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	if !e.gone("owned") {
		t.Fatal("the owned pool remains")
	}
}

func TestUnitUninstallRefusesToStrandPools(t *testing.T) {
	e := newEnv(t)
	e.managed("owned", 1)
	label(t, e, "owned", map[string]string{"app.kubernetes.io/instance": "alpha"})
	e.managed("foreign", 1)
	log := slog.New(slog.DiscardHandler)
	err := Uninstall(context.Background(), e.dyn, []string{namespace}, "app.kubernetes.io/instance=alpha", time.Millisecond, log)
	if !errors.Is(err, ErrUninstall) || !strings.Contains(err.Error(), namespace+"/foreign") {
		t.Fatalf("a foreign pool = %v", err)
	}
	if e.gone("owned") || e.pool("owned").DeletionTimestamp != nil {
		t.Fatal("an owned pool was deleted although the uninstallation was refused")
	}
	if err := Uninstall(context.Background(), e.dyn, []string{namespace}, "", time.Millisecond, log); !errors.Is(err, ErrUninstall) {
		t.Fatalf("an empty selector = %v", err)
	}
	if err := Uninstall(context.Background(), e.dyn, []string{namespace}, "a in (", time.Millisecond, log); !errors.Is(err, ErrUninstall) {
		t.Fatalf("an invalid selector = %v", err)
	}
}

func TestUnitUninstallNamesWhatRemains(t *testing.T) {
	e := newEnv(t)
	e.managed("owned", 1)
	label(t, e, "owned", map[string]string{"app.kubernetes.io/instance": "alpha"})
	e.change("owned", func(wp *WorkerPool) {
		wp.Finalizers = []string{Finalizer}
		meta.SetStatusCondition(&wp.Status.Conditions, condition(ConditionTerminating, false, ReasonCleanupBlocked, "a run executes", 1))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := Uninstall(ctx, e.dyn, []string{namespace}, "app.kubernetes.io/instance=alpha", time.Millisecond, slog.New(slog.DiscardHandler))
	if !errors.Is(err, ErrUninstall) || !strings.Contains(err.Error(), "CleanupBlocked: a run executes") {
		t.Fatalf("a pool that stays = %v", err)
	}
}

func TestUnitUninstallReportsKubernetesFailures(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	for _, verb := range []string{"list", "delete"} {
		t.Run(verb, func(t *testing.T) {
			e := newEnv(t)
			e.managed("owned", 1)
			label(t, e, "owned", map[string]string{"app.kubernetes.io/instance": "alpha"})
			e.dyn.PrependReactor(verb, "workerpools", func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, errors.New("refused by the test")
			})
			if err := Uninstall(context.Background(), e.dyn, []string{namespace}, "app.kubernetes.io/instance=alpha", time.Millisecond, log); !errors.Is(err, ErrUninstall) {
				t.Fatalf("a refused %s = %v", verb, err)
			}
		})
	}
	t.Run("undecodable", func(t *testing.T) {
		e := newEnv(t)
		e.managed("owned", 1)
		e.dyn.PrependReactor("list", "workerpools", func(k8stesting.Action) (bool, runtime.Object, error) {
			u, err := encode("WorkerPool", &WorkerPool{ObjectMeta: metav1.ObjectMeta{Name: "x", UID: types.UID("u")}})
			if err != nil {
				return true, nil, err
			}
			u.Object["spec"] = "not an object"
			return true, &unstructured.UnstructuredList{
				Object: map[string]any{"apiVersion": Group + "/" + Version, "kind": "WorkerPoolList"},
				Items:  []unstructured.Unstructured{*u},
			}, nil
		})
		if err := Uninstall(context.Background(), e.dyn, []string{namespace}, "app.kubernetes.io/instance=alpha", time.Millisecond, log); err == nil {
			t.Fatal("an undecodable pool was not reported")
		}
	})
}

// label sets the labels of the WorkerPool name.
func label(t *testing.T, e *env, name string, labels map[string]string) {
	t.Helper()
	e.change(name, func(wp *WorkerPool) { wp.Labels = labels })
}
