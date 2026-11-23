package controller

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

const (
	namespace = "workers"
	account   = "01KACME0000000000000000003"
	cluster   = "cluster-one"
)

// env is a controller's world: the fake installation, a fake Kubernetes
// API that honours finalizers, and a clock the test moves.
type env struct {
	t    *testing.T
	in   *installation
	dyn  *dynamicfake.FakeDynamicClient
	core *k8sfake.Clientset

	mu  sync.Mutex
	now time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		PoolResource: "WorkerPoolList", WorkerResource: "WorkerList",
	})
	e := &env{t: t, in: newInstallation(t), dyn: dyn, core: k8sfake.NewClientset(), now: time.Now()}
	e.finalizers()
	e.in.key("management", account)
	e.secret("management", map[string][]byte{"id": []byte("management"), "secret": []byte("secret-management")})
	return e
}

// finalizers makes the fake Kubernetes API keep a resource with
// finalizers, marked for deletion, until its last finalizer is removed,
// as the real one does.
func (e *env) finalizers() {
	tracker := e.dyn.Tracker()
	gvr := func(resource string) schema.GroupVersionResource {
		if resource == PoolResource.Resource {
			return PoolResource
		}
		return WorkerResource
	}
	e.dyn.PrependReactor("delete", "*", func(a k8stesting.Action) (bool, runtime.Object, error) {
		d, _ := a.(k8stesting.DeleteAction)
		obj, err := tracker.Get(gvr(d.GetResource().Resource), d.GetNamespace(), d.GetName())
		if err != nil {
			return false, nil, nil
		}
		u, _ := obj.(*unstructured.Unstructured)
		if len(u.GetFinalizers()) == 0 {
			return false, nil, nil
		}
		if u.GetDeletionTimestamp() == nil {
			now := metav1.Now()
			u.SetDeletionTimestamp(&now)
			if err := tracker.Update(gvr(d.GetResource().Resource), u, d.GetNamespace()); err != nil {
				return true, nil, err
			}
		}
		return true, u, nil
	})
	e.dyn.PrependReactor("create", "*", func(a k8stesting.Action) (bool, runtime.Object, error) {
		c, _ := a.(k8stesting.CreateAction)
		if u, ok := c.GetObject().(*unstructured.Unstructured); ok && u.GetUID() == "" {
			u.SetUID(types.UID(kernel.NewULID().String()))
		}
		return false, nil, nil
	})
	patch := k8stesting.ObjectReaction(tracker)
	e.dyn.PrependReactor("patch", "*", func(a k8stesting.Action) (bool, runtime.Object, error) {
		handled, obj, err := patch(a)
		if err != nil {
			return handled, obj, err
		}
		u, _ := obj.(*unstructured.Unstructured)
		if u != nil && u.GetDeletionTimestamp() != nil && len(u.GetFinalizers()) == 0 {
			err = tracker.Delete(gvr(a.GetResource().Resource), u.GetNamespace(), u.GetName())
		}
		return handled, obj, err
	})
}

func (e *env) clock() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

func (e *env) advance(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.now = e.now.Add(d)
}

func (e *env) settings() Settings {
	s := DefaultSettings()
	s.WatchNamespaces, s.ClusterID = []string{namespace}, cluster
	s.LeaderElection.Namespace = namespace
	s.ManagementCredentialsRef = &CredentialReference{Name: "management", IDKey: "id", SecretKey: "secret"}
	return s
}

func (e *env) controller(change ...func(*Settings)) *Controller {
	e.t.Helper()
	s := e.settings()
	for _, f := range change {
		f(&s)
	}
	c, err := New(Deps{
		Settings: s, Clients: Clients{Dynamic: e.dyn, Core: e.core.CoreV1(), Coordination: e.core.CoordinationV1()},
		HTTP: e.in.srv.Client(), Now: e.clock, Version: "test", Arch: "arm64", Identity: "replica",
		Resync: 10 * time.Millisecond,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func (e *env) secret(name string, data map[string][]byte) {
	e.t.Helper()
	ctx := context.Background()
	s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}, Data: data}
	if _, err := e.core.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{}); err == nil {
		_, err = e.core.CoreV1().Secrets(namespace).Update(ctx, s, metav1.UpdateOptions{})
		if err != nil {
			e.t.Fatal(err)
		}
		return
	}
	if _, err := e.core.CoreV1().Secrets(namespace).Create(ctx, s, metav1.CreateOptions{}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) secretData(name string) map[string][]byte {
	e.t.Helper()
	s, err := e.core.CoreV1().Secrets(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		e.t.Fatal(err)
	}
	return s.Data
}

func newKey(t *testing.T) (*ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}

func ptr[T any](v T) *T { return &v }

// managed creates a Managed WorkerPool name of capacity whose credential
// Secret holds a fresh private key.
func (e *env) managed(name string, capacity int32) WorkerPool {
	_, key := newKey(e.t)
	e.secret(name+"-credentials", map[string][]byte{PrivateKeyKey: key})
	return e.create(WorkerPool{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: PoolSpec{
			Endpoint: e.in.srv.URL, Account: ptr(account), Space: ptr("space"), Mode: Managed,
			CredentialsRef: corev1.LocalObjectReference{Name: name + "-credentials"}, Capacity: capacity,
			Name: ptr(name), Description: ptr("managed"), Labels: &[]string{"k8s"},
		},
	})
}

// hosted creates an Existing or Public WorkerPool name of capacity over
// a pool the installation already holds.
func (e *env) hosted(name, mode string, capacity int32) (WorkerPool, *remotePool) {
	key, pemKey := newKey(e.t)
	p := e.in.pool(account, key)
	e.secret(name+"-credentials", map[string][]byte{PrivateKeyKey: pemKey, TokenKey: []byte(p.token)})
	spec := PoolSpec{
		Endpoint: e.in.srv.URL, Mode: mode, PoolID: ptr(p.id),
		CredentialsRef: corev1.LocalObjectReference{Name: name + "-credentials"}, Capacity: capacity,
	}
	if mode == Existing {
		spec.Account = ptr(account)
	}
	return e.create(WorkerPool{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: spec}), p
}

func (e *env) create(wp WorkerPool) WorkerPool {
	e.t.Helper()
	wp.Namespace, wp.UID, wp.Generation = namespace, types.UID(kernel.NewULID().String()), 1
	u, err := encode("WorkerPool", &wp)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.dyn.Resource(PoolResource).Namespace(namespace).Create(context.Background(), u, metav1.CreateOptions{}); err != nil {
		e.t.Fatal(err)
	}
	return e.pool(wp.Name)
}

func (e *env) pool(name string) WorkerPool {
	e.t.Helper()
	u, err := e.dyn.Resource(PoolResource).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		e.t.Fatal(err)
	}
	wp, err := decode[WorkerPool](u)
	if err != nil {
		e.t.Fatal(err)
	}
	return wp
}

func (e *env) gone(name string) bool {
	_, err := e.dyn.Resource(PoolResource).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
	return err != nil
}

func (e *env) change(name string, f func(*WorkerPool)) {
	e.t.Helper()
	wp := e.pool(name)
	f(&wp)
	u, err := encode("WorkerPool", &wp)
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.dyn.Resource(PoolResource).Namespace(namespace).Update(context.Background(), u, metav1.UpdateOptions{}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) remove(name string) {
	e.t.Helper()
	if err := e.dyn.Resource(PoolResource).Namespace(namespace).Delete(context.Background(), name, metav1.DeleteOptions{}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) workers(name string) []Worker {
	e.t.Helper()
	c := e.controller()
	out, err := c.workers(context.Background(), e.pool(name))
	if err != nil {
		e.t.Fatal(err)
	}
	slices.SortFunc(out, func(a, b Worker) int { return int(a.Spec.Slot - b.Spec.Slot) })
	return out
}

func (e *env) pass(c *Controller) {
	e.t.Helper()
	c.Pass(context.Background())
}

// expect fails unless wp's condition typ has status and reason.
func expect(t *testing.T, conditions []metav1.Condition, typ string, status metav1.ConditionStatus, reason string) {
	t.Helper()
	c := meta.FindStatusCondition(conditions, typ)
	if c == nil || c.Status != status || c.Reason != reason {
		t.Fatalf("condition %s = %+v, want %s %s", typ, c, status, reason)
	}
}

func workerIDs(ws []Worker) []string {
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		out = append(out, w.Status.WorkerID)
	}
	return out
}

func TestUnitManagedPoolConvergesAcrossReplicasAndRestarts(t *testing.T) {
	e := newEnv(t)
	e.managed("build", 2)
	e.pass(e.controller())

	live := e.in.live()
	if len(live) != 1 {
		t.Fatalf("remote pools = %d, want 1", len(live))
	}
	remote := live[0]
	wp := e.pool("build")
	if !hasFinalizer(wp.ObjectMeta) || wp.Status.PoolID != remote.id || wp.Status.Owner == nil || *wp.Status.Owner != wp.Owner(cluster) {
		t.Fatalf("pool = %+v, want finalizer, pool %s and its owner", wp, remote.id)
	}
	if remote.management == nil || *remote.management != wp.Owner(cluster) || remote.settings.Name != "build" {
		t.Fatalf("remote pool management = %+v settings = %+v", remote.management, remote.settings)
	}
	if string(e.secretData("build-credentials")[TokenKey]) != remote.token {
		t.Fatal("the pool token was not written to the credential Secret")
	}
	expect(t, wp.Status.Conditions, ConditionBound, metav1.ConditionTrue, ReasonBound)
	expect(t, wp.Status.Conditions, ConditionRegistered, metav1.ConditionTrue, ReasonRegistered)
	expect(t, wp.Status.Conditions, ConditionExecutor, metav1.ConditionFalse, ReasonExecutorUnavailable)
	if wp.Status.Desired != 2 || wp.Status.Registered != 2 {
		t.Fatalf("status counts = %+v", wp.Status)
	}
	ws := e.workers("build")
	if len(ws) != 2 || ws[0].Name != SlotName(string(wp.UID), 0) || ws[1].Spec.Slot != 1 || !hasFinalizer(ws[0].ObjectMeta) ||
		ws[0].OwnerReferences[0].UID != wp.UID || ws[0].Status.WorkerID == "" {
		t.Fatalf("workers = %+v", ws)
	}
	ids := workerIDs(ws)
	for _, c := range e.in.registrations() {
		if c.Owner != wp.Owner(cluster) || c.Slot == nil || !slices.Equal(c.Capabilities, []string{"plan-sanitization-v1"}) || c.Version != "test" {
			t.Fatalf("registration claims = %+v", c)
		}
	}

	// A second replica and a restarted one resume the same pool and slots.
	for range 2 {
		e.pass(e.controller())
		if len(e.in.live()) != 1 || !slices.Equal(workerIDs(e.workers("build")), ids) || len(e.in.get(remote.id).slots) != 2 {
			t.Fatalf("a replica did not converge: pools %d, workers %v, want %v", len(e.in.live()), workerIDs(e.workers("build")), ids)
		}
	}
	if e.in.count("create") != 1 {
		t.Fatalf("creates = %d, want 1", e.in.count("create"))
	}
}

func TestUnitManagedPoolReplaysALostCreation(t *testing.T) {
	e := newEnv(t)
	e.managed("build", 1)
	c := e.controller()
	e.in.refusing("bind", http.StatusServiceUnavailable)
	e.pass(c)
	expect(t, e.pool("build").Status.Conditions, ConditionBound, metav1.ConditionFalse, ReasonUnavailable)
	e.in.refusing("bind", 0)
	e.pass(c)
	if len(e.in.live()) != 1 || e.in.count("create") != 1 {
		t.Fatalf("pools %d creates %d, want 1 and 1", len(e.in.live()), e.in.count("create"))
	}
	expect(t, e.pool("build").Status.Conditions, ConditionBound, metav1.ConditionTrue, ReasonBound)
}

func TestUnitManagedPoolFollowsItsSettings(t *testing.T) {
	e := newEnv(t)
	e.managed("build", 0)
	c := e.controller()
	e.pass(c)
	e.pass(c)
	if e.in.count("update") != 0 {
		t.Fatalf("updates of unchanged settings = %d", e.in.count("update"))
	}
	e.change("build", func(wp *WorkerPool) {
		wp.Spec.Description, wp.Spec.Labels, wp.Spec.PreparingTimeoutSeconds = ptr("changed"), nil, ptr(int32(600))
	})
	e.pass(c)
	remote := e.in.live()[0]
	if e.in.count("update") != 1 || *remote.settings.Description != "changed" || *remote.settings.PreparingTimeoutSeconds != 600 ||
		e.in.ifMatch[0] != `"`+remote.id+`-0"` {
		t.Fatalf("updates %d settings %+v If-Match %v", e.in.count("update"), remote.settings, e.in.ifMatch)
	}
}

func TestUnitManagedPoolIsNeverRecreatedAfterItsDeletion(t *testing.T) {
	e := newEnv(t)
	e.managed("build", 1)
	c := e.controller()
	e.pass(c)
	remote := e.in.live()[0]
	e.in.mu.Lock()
	e.in.pools[remote.id].deleted = true
	e.in.mu.Unlock()
	e.pass(c)
	if len(e.in.live()) != 0 || e.in.count("create") != 1 {
		t.Fatal("a deleted managed pool was recreated")
	}
	expect(t, e.pool("build").Status.Conditions, ConditionBound, metav1.ConditionFalse, ReasonRemotePoolMissing)

	// Deleting the resource then has no remote worker to deregister.
	e.remove("build")
	e.pass(c)
	if !e.gone("build") {
		t.Fatal("the resource of a deleted remote pool kept its finalizer")
	}
}

func TestUnitManagedPoolRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(*env)
		change func(*Settings)
		reason string
	}{
		{"no management reference", nil, func(s *Settings) { s.ManagementCredentialsRef = nil }, ReasonManagementMissing},
		{"no management Secret", nil, func(s *Settings) { s.ManagementCredentialsRef.Name = "absent" }, ReasonManagementMissing},
		{"management key of another account", func(e *env) {
			e.in.key("other", "01KG10BEX00000000000000003")
			e.secret("management", map[string][]byte{"id": []byte("other"), "secret": []byte("secret-other")})
		}, nil, ReasonAccountMismatch},
		{"refused management key", func(e *env) {
			e.secret("management", map[string][]byte{"id": []byte("management"), "secret": []byte("wrong")})
		}, nil, ReasonCredentialsRejected},
		{"no credential Secret", func(e *env) {
			if err := e.core.CoreV1().Secrets(namespace).Delete(context.Background(), "build-credentials", metav1.DeleteOptions{}); err != nil {
				e.t.Fatal(err)
			}
		}, nil, ReasonCredentialsMissing},
		{"a private key that is not PEM", func(e *env) {
			e.secret("build-credentials", map[string][]byte{PrivateKeyKey: []byte("not a key")})
		}, nil, ReasonCredentialsInvalid},
		{"creation forbidden", func(e *env) { e.in.refusing("create", http.StatusForbidden) }, nil, ReasonForbidden},
		{"lookup unavailable", func(e *env) { e.in.refusing("managed", http.StatusServiceUnavailable) }, nil, ReasonUnavailable},
		{"creation invalid", func(e *env) { e.in.refusing("create", http.StatusUnprocessableEntity) }, nil, ReasonRefused},
		{"account unreadable", func(e *env) { e.in.refusing("id", http.StatusUnauthorized) }, nil, ReasonCredentialsRejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.managed("build", 1)
			if tc.setup != nil {
				tc.setup(e)
			}
			var change []func(*Settings)
			if tc.change != nil {
				change = append(change, tc.change)
			}
			e.pass(e.controller(change...))
			expect(t, e.pool("build").Status.Conditions, ConditionBound, metav1.ConditionFalse, tc.reason)
			if len(e.workers("build")) != 0 {
				t.Fatal("a refused pool created workers")
			}
		})
	}
}

func TestUnitHostedPoolsBindAndRegister(t *testing.T) {
	e := newEnv(t)
	_, existing := e.hosted("existing", Existing, 1)
	_, public := e.hosted("public", Public, 1)
	e.pass(e.controller())
	for name, remote := range map[string]*remotePool{"existing": existing, "public": public} {
		wp := e.pool(name)
		expect(t, wp.Status.Conditions, ConditionBound, metav1.ConditionTrue, ReasonBound)
		if got := e.in.get(remote.id); got.binding == nil || *got.binding != wp.Owner(cluster) || len(got.slots) != 1 {
			t.Fatalf("%s binding = %+v slots = %v", name, got.binding, got.slots)
		}
	}
	for _, c := range e.in.registrations() {
		want := []string{"plan-sanitization-v1"}
		if c.Pool == public.id {
			want = append(want, "public-pool-v1")
		}
		if !slices.Equal(c.Capabilities, want) {
			t.Fatalf("capabilities of %s = %v, want %v", c.Pool, c.Capabilities, want)
		}
	}
	if e.in.count("token") != 0 {
		t.Fatal("a hosted pool exchanged the management key")
	}
}

func TestUnitHostedPoolRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(*env, *remotePool)
		reason string
	}{
		{"bound to another owner", func(e *env, p *remotePool) {
			e.in.mu.Lock()
			p.binding = &kernel.WorkerOwner{ClusterID: "cluster-two", Namespace: namespace, ResourceUID: "other"}
			e.in.mu.Unlock()
		}, ReasonOwnershipConflict},
		{"a pool another resource manages", func(e *env, p *remotePool) {
			e.in.mu.Lock()
			p.management = &kernel.WorkerOwner{ClusterID: cluster, Namespace: namespace, ResourceUID: "manager"}
			e.in.mu.Unlock()
		}, ReasonOwnershipConflict},
		{"a token of another pool", func(e *env, _ *remotePool) {
			e.change("existing", func(wp *WorkerPool) { wp.Spec.PoolID = ptr(kernel.NewULID().String()) })
		}, ReasonCredentialsInvalid},
		{"no token", func(e *env, _ *remotePool) {
			_, key := newKey(e.t)
			e.secret("existing-credentials", map[string][]byte{PrivateKeyKey: key})
		}, ReasonCredentialsMissing},
		{"a key the token does not certify", func(e *env, p *remotePool) {
			_, key := newKey(e.t)
			e.secret("existing-credentials", map[string][]byte{PrivateKeyKey: key, TokenKey: []byte(p.token)})
		}, ReasonCredentialsInvalid},
		{"an unknown pool", func(e *env, p *remotePool) {
			e.in.mu.Lock()
			p.deleted = true
			e.in.mu.Unlock()
		}, ReasonCredentialsRejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			_, p := e.hosted("existing", Existing, 1)
			tc.setup(e, p)
			e.pass(e.controller())
			expect(t, e.pool("existing").Status.Conditions, ConditionBound, metav1.ConditionFalse, tc.reason)
			if len(e.in.get(p.id).slots) != 0 {
				t.Fatal("a refused pool registered a worker")
			}
		})
	}
}

func TestUnitOrganizationLimitBoundsEveryPool(t *testing.T) {
	e := newEnv(t)
	e.in.setLimit(3)
	_, first := e.hosted("first", Existing, 2)
	_, second := e.hosted("second", Existing, 5)
	c := e.controller()
	e.pass(c)
	e.pass(c)
	if n := len(e.in.get(first.id).slots) + len(e.in.get(second.id).slots); n != 3 {
		t.Fatalf("registered workers = %d, want the limit of 3", n)
	}
	limited := e.pool("second")
	if limited.Name == "first" {
		limited = e.pool("first")
	}
	expect(t, limited.Status.Conditions, ConditionRegistered, metav1.ConditionFalse, ReasonLimitReached)
	if got := len(e.workers("second")); got != 2 {
		t.Fatalf("worker resources of the limited pool = %d, want its registered slot and one refused", got)
	}
	e.in.setLimit(7)
	e.pass(c)
	e.pass(c)
	expect(t, e.pool("second").Status.Conditions, ConditionRegistered, metav1.ConditionTrue, ReasonRegistered)
	if len(e.in.get(second.id).slots) != 5 {
		t.Fatalf("slots after the limit rose = %d", len(e.in.get(second.id).slots))
	}
}

func TestUnitCapacityChangesRetireSlots(t *testing.T) {
	e := newEnv(t)
	_, p := e.hosted("existing", Existing, 3)
	c := e.controller()
	e.pass(c)
	e.change("existing", func(wp *WorkerPool) { wp.Spec.Capacity = 1 })
	e.pass(c)
	if len(e.workers("existing")) != 1 || len(e.in.get(p.id).slots) != 1 || e.in.count("deregister") != 2 {
		t.Fatalf("workers %d slots %d deregistrations %d", len(e.workers("existing")), len(e.in.get(p.id).slots), e.in.count("deregister"))
	}
	if wp := e.pool("existing"); wp.Status.Registered != 1 || wp.Status.Retiring != 2 {
		t.Fatalf("status = %+v", wp.Status)
	}

	// A Worker deleted by hand is retired and its slot comes back.
	w := e.workers("existing")[0]
	if err := e.dyn.Resource(WorkerResource).Namespace(namespace).Delete(context.Background(), w.Name, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	e.pass(c)
	e.pass(c)
	if ws := e.workers("existing"); len(ws) != 1 || ws[0].UID == w.UID && ws[0].DeletionTimestamp != nil {
		t.Fatalf("workers = %+v", ws)
	}
}

func TestUnitRegisteredSlotsHeartbeat(t *testing.T) {
	e := newEnv(t)
	e.hosted("existing", Existing, 2)
	c := e.controller()
	e.pass(c)
	n := len(e.in.registrations())
	e.pass(c)
	if len(e.in.registrations()) != n {
		t.Fatal("a slot registered again before its heartbeat")
	}
	e.advance(DefaultHeartbeat)
	e.pass(c)
	if len(e.in.registrations()) != n+2 {
		t.Fatalf("registrations = %d, want %d", len(e.in.registrations()), n+2)
	}
}

func TestUnitDeletingAManagedPoolCleansUp(t *testing.T) {
	e := newEnv(t)
	e.managed("build", 2)
	c := e.controller()
	e.pass(c)
	remote := e.in.live()[0]
	e.remove("build")

	e.in.refusing("deregister", http.StatusServiceUnavailable)
	e.pass(c)
	wp := e.pool("build")
	expect(t, wp.Status.Conditions, ConditionTerminating, metav1.ConditionFalse, ReasonCleanupBlocked)
	if len(e.in.live()) != 1 {
		t.Fatal("the remote pool was deleted while workers remained")
	}
	for _, w := range e.workers("build") {
		expect(t, w.Status.Conditions, ConditionTerminating, metav1.ConditionFalse, ReasonCleanupBlocked)
	}

	e.in.refusing("deregister", 0)
	e.in.refusing("delete", http.StatusConflict)
	e.pass(c)
	expect(t, e.pool("build").Status.Conditions, ConditionTerminating, metav1.ConditionFalse, ReasonOwnershipConflict)

	e.in.refusing("delete", 0)
	e.pass(c)
	if !e.gone("build") || len(e.in.live()) != 0 || len(e.in.get(remote.id).slots) != 0 {
		t.Fatalf("cleanup left: resource %v, remote pools %d", !e.gone("build"), len(e.in.live()))
	}
}

func TestUnitDeletingAHostedPoolReleasesIt(t *testing.T) {
	e := newEnv(t)
	_, p := e.hosted("existing", Existing, 1)
	c := e.controller()
	e.pass(c)
	e.remove("existing")
	e.in.refusing("release", http.StatusConflict)
	e.pass(c)
	expect(t, e.pool("existing").Status.Conditions, ConditionTerminating, metav1.ConditionFalse, ReasonOwnershipConflict)
	e.in.refusing("release", 0)
	e.pass(c)
	if !e.gone("existing") || e.in.get(p.id).binding != nil || e.in.get(p.id).deleted {
		t.Fatalf("release left the resource %v, binding %+v", !e.gone("existing"), e.in.get(p.id).binding)
	}
}

func TestUnitDeletingAPoolThatNeverBoundReleasesItsFinalizer(t *testing.T) {
	e := newEnv(t)
	e.hosted("existing", Existing, 1)
	c := e.controller()
	e.in.refusing("bind", http.StatusForbidden)
	e.pass(c)
	e.remove("existing")
	e.pass(c)
	if !e.gone("existing") {
		t.Fatal("a pool that never bound kept its finalizer")
	}
}

func TestUnitDeletingAPoolWithBrokenCredentialsWaits(t *testing.T) {
	e := newEnv(t)
	e.hosted("existing", Existing, 1)
	c := e.controller()
	e.pass(c)
	e.secret("existing-credentials", map[string][]byte{PrivateKeyKey: []byte("broken")})
	e.remove("existing")
	e.pass(c)
	expect(t, e.pool("existing").Status.Conditions, ConditionTerminating, metav1.ConditionFalse, ReasonCredentialsInvalid)
}

func TestUnitRemoteFaultsClassifyStatus(t *testing.T) {
	for status, reason := range map[int]string{
		http.StatusUnauthorized: ReasonCredentialsRejected, http.StatusForbidden: ReasonForbidden,
		http.StatusConflict: ReasonOwnershipConflict, http.StatusTooManyRequests: ReasonUnavailable,
		http.StatusBadGateway: ReasonUnavailable, http.StatusBadRequest: ReasonRefused,
	} {
		if got := remoteFault("op", &client.Error{Op: "op", Status: status}).reason; got != reason {
			t.Errorf("status %d = %s, want %s", status, got, reason)
		}
	}
	if got := remoteFault("op", &client.Error{Status: http.StatusForbidden, Type: "urn:infraweave:problem:limit-reached"}).reason; got != ReasonLimitReached {
		t.Errorf("limit = %s", got)
	}
	if got := remoteFault("op", errors.New("dial")); got.reason != ReasonUnavailable || !errors.Is(got, got.err) {
		t.Errorf("transport = %+v", got)
	}
	if got := asFault(errors.New("plain")); got.reason != ReasonUnavailable {
		t.Errorf("plain = %+v", got)
	}
}

func TestUnitUnreachableInstallationIsVisible(t *testing.T) {
	e := newEnv(t)
	e.hosted("existing", Existing, 1)
	e.in.srv.Close()
	e.pass(e.controller())
	expect(t, e.pool("existing").Status.Conditions, ConditionBound, metav1.ConditionFalse, ReasonUnavailable)
}

func TestUnitPrivateKeysParse(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := privateKey(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})); err != nil {
		t.Fatal(err)
	}
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der384, err := x509.MarshalECPrivateKey(p384)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsaDER, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string][]byte{
		"P-384":     pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der384}),
		"RSA":       pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rsaDER}),
		"other PEM": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1}}),
		"bad DER":   pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: []byte{1}}),
	} {
		if _, err := privateKey(raw); !errors.Is(err, ErrCredentials) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestUnitPoolTokensMustCertifyTheirPool(t *testing.T) {
	key, _ := newKey(t)
	for name, token := range map[string]string{
		"not base64":   "%%%",
		"not JSON":     "bm90IGpzb24=",
		"version 2":    "eyJ2ZXJzaW9uIjoyLCJwb29sIjoicCJ9",
		"no cert":      "eyJ2ZXJzaW9uIjoxLCJwb29sIjoicCJ9",
		"bad cert DER": "eyJ2ZXJzaW9uIjoxLCJwb29sIjoicCIsImNlcnRpZmljYXRlIjoiLS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tXG5BUT09XG4tLS0tLUVORCBDRVJUSUZJQ0FURS0tLS0tXG4ifQ==",
	} {
		if _, err := newIdentity(token, key, ""); !errors.Is(err, ErrCredentials) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestUnitControllerLeadsAndReconciles(t *testing.T) {
	e := newEnv(t)
	e.hosted("existing", Existing, 1)
	c := e.controller()
	if err := c.Synced(context.Background()); err != nil {
		t.Fatalf("a standby replica is not ready: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	deadline := time.After(20 * time.Second)
	for {
		wp := e.pool("existing")
		if c.Leading() && meta.IsStatusConditionTrue(wp.Status.Conditions, ConditionRegistered) && c.Synced(ctx) == nil {
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatal("the controller did not lead and reconcile")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestUnitControllerPassSurvivesUnreadableNamespaces(t *testing.T) {
	e := newEnv(t)
	e.dyn.PrependReactor("list", "workerpools", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})
	c := e.controller()
	c.leading.Store(true)
	e.pass(c)
	if c.Synced(context.Background()) == nil {
		t.Fatal("a replica that could not list its pools is ready")
	}
}

func TestUnitHTTPClientTrustsItsCAFile(t *testing.T) {
	if _, err := httpClient("", requestTimeout); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := New(Deps{Settings: Settings{CAFile: dir + "/absent.pem"}}); !errors.Is(err, ErrSettings) {
		t.Fatalf("missing ca_file = %v", err)
	}
	e := newEnv(t)
	cert := e.in.get(e.in.pool(account, mustKey(t)).id).cert
	path := dir + "/ca.pem"
	if err := writeFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})); err != nil {
		t.Fatal(err)
	}
	if c, err := httpClient(path, requestTimeout); err != nil || c.Transport == nil {
		t.Fatalf("ca_file = %v", err)
	}
	if err := writeFile(path, []byte("no PEM")); err != nil {
		t.Fatal(err)
	}
	if _, err := httpClient(path, requestTimeout); !errors.Is(err, ErrSettings) {
		t.Fatalf("empty ca_file = %v", err)
	}
}

func mustKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, _ := newKey(t)
	return key
}

func writeFile(path string, b []byte) error { return os.WriteFile(path, b, 0o600) }

func TestUnitKubernetesFailuresAreReported(t *testing.T) {
	for _, tc := range []struct{ verb, resource, subresource string }{
		{"list", "workers", ""},
		{"create", "workers", ""},
		{"patch", "workerpools", ""},
		{"patch", "workerpools", "status"},
		{"patch", "workers", "status"},
		{"patch", "secrets", ""},
		{"get", "secrets", ""},
	} {
		t.Run(tc.verb+" "+tc.resource+" "+tc.subresource, func(t *testing.T) {
			e := newEnv(t)
			e.managed("build", 1)
			fail := func(a k8stesting.Action) (bool, runtime.Object, error) {
				if a.GetSubresource() != tc.subresource {
					return false, nil, nil
				}
				return true, nil, errors.New("refused by the test")
			}
			e.dyn.PrependReactor(tc.verb, tc.resource, fail)
			e.core.PrependReactor(tc.verb, tc.resource, fail)
			c := e.controller()
			if err := c.reconcile(context.Background(), e.pool("build")); err == nil {
				t.Fatal("a refused Kubernetes call was not reported")
			}
		})
	}
}

func TestUnitRetiringFailuresAreReported(t *testing.T) {
	for _, tc := range []struct{ verb, subresource string }{{"delete", ""}, {"patch", ""}} {
		t.Run(tc.verb, func(t *testing.T) {
			e := newEnv(t)
			e.hosted("existing", Existing, 1)
			c := e.controller()
			e.pass(c)
			e.change("existing", func(wp *WorkerPool) { wp.Spec.Capacity = 0 })
			e.dyn.PrependReactor(tc.verb, "workers", func(a k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, errors.New("refused by the test")
			})
			if err := c.reconcile(context.Background(), e.pool("existing")); err == nil {
				t.Fatal("a refused retirement was not reported")
			}
		})
	}
}

func TestUnitSettingsDriftComparesEveryField(t *testing.T) {
	wp := WorkerPool{Spec: PoolSpec{
		Name: ptr("n"), Space: ptr("s"), Description: ptr("d"), Labels: &[]string{"a"},
		PreparingTimeoutSeconds: ptr(int32(1)), DriftDetectionRunLimit: ptr(int32(2)),
	}}
	want := settings(wp)
	same := view(&remotePool{settings: want})
	if drifted(same, want) {
		t.Fatal("equal settings drifted")
	}
	for name, change := range map[string]func(*WorkerPool){
		"name":    func(wp *WorkerPool) { wp.Spec.Name = ptr("m") },
		"space":   func(wp *WorkerPool) { wp.Spec.Space = nil },
		"labels":  func(wp *WorkerPool) { wp.Spec.Labels = &[]string{"b"} },
		"timeout": func(wp *WorkerPool) { wp.Spec.PreparingTimeoutSeconds = ptr(int32(3)) },
		"drift":   func(wp *WorkerPool) { wp.Spec.DriftDetectionRunLimit = ptr(int32(3)) },
	} {
		changed := wp
		change(&changed)
		if !drifted(same, settings(changed)) {
			t.Errorf("a changed %s did not drift", name)
		}
	}
	unset := wp
	unset.Spec.PreparingTimeoutSeconds, unset.Spec.Labels = nil, nil
	if drifted(view(&remotePool{settings: settings(unset)}), settings(unset)) {
		t.Fatal("unset settings drifted")
	}
}

func TestUnitManagedUpdateRefusalIsVisible(t *testing.T) {
	e := newEnv(t)
	e.managed("build", 0)
	c := e.controller()
	e.pass(c)
	e.change("build", func(wp *WorkerPool) { wp.Spec.Description = ptr("changed") })
	e.in.refusing("update", http.StatusPreconditionFailed)
	e.pass(c)
	expect(t, e.pool("build").Status.Conditions, ConditionBound, metav1.ConditionFalse, ReasonRefused)
}

func TestUnitStatusCopiesShareNothing(t *testing.T) {
	ws := WorkerStatus{Assignment: &AssignmentStatus{RunID: "r"}, Conditions: []metav1.Condition{{Type: "a"}}}
	c := ws.DeepCopy()
	c.Assignment.RunID, c.Conditions[0].Type = "s", "b"
	if ws.Assignment.RunID != "r" || ws.Conditions[0].Type != "a" {
		t.Fatal("a copy shares its status")
	}
}
