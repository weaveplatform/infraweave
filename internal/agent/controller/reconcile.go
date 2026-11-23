package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"unicode/utf8"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/weaveplatform/infraweave/internal/agent/client"
	"github.com/weaveplatform/infraweave/internal/agent/wire/management"
	"github.com/weaveplatform/infraweave/internal/kernel"
)

// The condition types a WorkerPool and a Worker report.
const (
	// ConditionBound is True once the pool's credentials are verified and
	// its hosting binding names this resource.
	ConditionBound = "Bound"
	// ConditionRegistered is True on a pool once every slot of its
	// capacity holds a registered worker, and on a Worker once it does.
	ConditionRegistered = "Registered"
	// ConditionExecutor is True on a pool while the controller can execute
	// runs in Pods, and on a Worker while its slot's launcher polls for
	// runs; False, registered slots claim no runs.
	ConditionExecutor = "ExecutorAvailable"
	// ConditionTerminating reports the cleanup of a deleted pool or slot.
	ConditionTerminating = "Terminating"
	// ConditionConnected is True on a Worker while the installation
	// answers its slot's launcher's polls, and False once it has answered
	// none within three poll timeouts: the run's Pod goes on while the
	// launcher retries, until the server's run.lost_after fails the run as
	// worker lost.
	ConditionConnected = "Connected"
	// ConditionDrained is True on a Worker while the installation drains
	// it: it finishes its run and claims no other.
	ConditionDrained = "Drained"
)

// The reasons a condition carries.
const (
	ReasonBound               = "Bound"
	ReasonCredentialsMissing  = "CredentialsMissing"
	ReasonCredentialsInvalid  = "CredentialsInvalid"
	ReasonCredentialsRejected = "CredentialsRejected"
	ReasonManagementMissing   = "ManagementCredentialsMissing"
	ReasonAccountMismatch     = "AccountMismatch"
	ReasonForbidden           = "Forbidden"
	ReasonOwnershipConflict   = "OwnershipConflict"
	ReasonRemotePoolMissing   = "RemotePoolMissing"
	ReasonRefused             = "Refused"
	ReasonUnavailable         = "RemoteUnavailable"
	ReasonRegistered          = "Registered"
	ReasonRegistering         = "Registering"
	ReasonLimitReached        = "LimitReached"
	ReasonExecutorUnavailable = "ExecutorUnavailable"
	ReasonCleaning            = "Cleaning"
	ReasonCleanupBlocked      = "CleanupBlocked"
	ReasonPolling             = "Polling"
	ReasonServerUnreachable   = "ServerUnreachable"
	ReasonDrained             = "Drained"
	ReasonAccepting           = "AcceptingRuns"
)

// fault is a reconciliation failure as a condition reports it.
// unreached marks a call to the installation that no healthy server
// answered, which the readiness probe reports.
type fault struct {
	reason, message string
	err             error
	unreached       bool
}

func (f *fault) Error() string { return f.reason + ": " + f.message }
func (f *fault) Unwrap() error { return f.err }

func fail(reason, message string, err error) *fault {
	return &fault{reason: reason, message: message, err: err}
}

// remoteFault classifies err, a call to the installation, by its status:
// a refused credential, a missing permission, an ownership conflict, a
// refusal of the request, or an outage that the next pass retries. A
// refusal's message names its call; op names a call that never answered.
func remoteFault(op string, err error) *fault {
	var e *client.Error
	if !errors.As(err, &e) {
		f := fail(ReasonUnavailable, op+": the installation is unreachable.", err)
		f.unreached = true
		return f
	}
	message := e.Error()
	switch {
	case e.Status == http.StatusUnauthorized:
		return fail(ReasonCredentialsRejected, message, err)
	case e.Status == http.StatusForbidden && e.Type == "urn:infraweave:problem:limit-reached":
		return fail(ReasonLimitReached, message, err)
	case e.Status == http.StatusForbidden:
		return fail(ReasonForbidden, message, err)
	case e.Status == http.StatusConflict:
		return fail(ReasonOwnershipConflict, message, err)
	case e.Status >= http.StatusInternalServerError:
		f := fail(ReasonUnavailable, message, err)
		f.unreached = true
		return f
	case e.Status == http.StatusTooManyRequests:
		return fail(ReasonUnavailable, message, err)
	}
	return fail(ReasonRefused, message, err)
}

func asFault(err error) *fault {
	var f *fault
	if errors.As(err, &f) {
		return f
	}
	return fail(ReasonUnavailable, err.Error(), err)
}

func condition(typ string, ok bool, reason, message string, generation int64) metav1.Condition {
	status := metav1.ConditionFalse
	if ok {
		status = metav1.ConditionTrue
	}
	return metav1.Condition{Type: typ, Status: status, Reason: reason, Message: message, ObservedGeneration: generation}
}

// hosted is a pool the controller verified it may host: the remote, the
// signing identity, the pool token and private key PEM it was read from,
// which a slot's launcher registers with, and the resource owner.
type hosted struct {
	remote *remote
	id     identity
	token  string
	key    []byte
	owner  kernel.WorkerOwner
	public bool
}

// secret reads keys of the Secret name in namespace; a missing Secret or
// key is a fault of reason.
func (c *Controller) secret(ctx context.Context, namespace, name, reason string, keys ...string) (map[string][]byte, error) {
	s, err := c.kube.Core.Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, fail(reason, fmt.Sprintf("Secret %s/%s does not exist.", namespace, name), err)
	}
	if err != nil {
		return nil, fail(ReasonUnavailable, fmt.Sprintf("Secret %s/%s cannot be read.", namespace, name), err)
	}
	for _, k := range keys {
		if len(s.Data[k]) == 0 {
			return nil, fail(reason, fmt.Sprintf("Secret %s/%s has no %s key.", namespace, name, k), nil)
		}
	}
	return s.Data, nil
}

// managementKey reads the management API key the configuration names in
// wp's namespace.
func (c *Controller) managementKey(ctx context.Context, namespace string) (apiKey, error) {
	ref := c.settings.ManagementCredentialsRef
	if ref == nil {
		return apiKey{}, fail(ReasonManagementMissing, "Managed pools need worker_controller.management_credentials_ref.", nil)
	}
	data, err := c.secret(ctx, namespace, ref.Name, ReasonManagementMissing, ref.IDKey, ref.SecretKey)
	if err != nil {
		return apiKey{}, err
	}
	return apiKey{id: string(data[ref.IDKey]), secret: string(data[ref.SecretKey])}, nil
}

// session is an API token of the management key for r, verified to belong
// to wp's account.
func (c *Controller) session(ctx context.Context, wp WorkerPool, r *remote) (string, error) {
	key, err := c.managementKey(ctx, wp.Namespace)
	if err != nil {
		return "", err
	}
	jwt, err := r.token(ctx, key)
	if err != nil {
		return "", remoteFault("exchange the management key", err)
	}
	account, err := r.account(ctx, jwt)
	if err != nil {
		if client.Status(err) == http.StatusUnauthorized {
			r.forget(key)
		}
		return "", remoteFault("read the management account", err)
	}
	if wp.Spec.Account == nil || account != *wp.Spec.Account {
		return "", fail(ReasonAccountMismatch, "The management key belongs to another account than spec.account.", nil)
	}
	return jwt, nil
}

// settings are the private pool settings of a Managed resource, which
// replace the remote pool's when they differ.
func settings(wp WorkerPool) management.WorkerPoolUpdateRequest {
	s := wp.Spec
	out := management.WorkerPoolUpdateRequest{
		Space: s.Space, Description: s.Description, Labels: s.Labels,
		PreparingTimeoutSeconds: s.PreparingTimeoutSeconds, DriftDetectionRunLimit: s.DriftDetectionRunLimit,
	}
	if s.Name != nil {
		out.Name = *s.Name
	}
	return out
}

// drifted reports whether the remote pool's settings differ from want.
// A timeout or drift limit the resource leaves unset is the server's.
func drifted(remote management.WorkerPool, want management.WorkerPoolUpdateRequest) bool {
	text := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	labels := func(l *[]string) []string {
		if l == nil {
			return nil
		}
		return *l
	}
	same := func(want, got *int32) bool { return want == nil || got != nil && *want == *got }
	return remote.Name != want.Name || remote.Space != text(want.Space) ||
		text(remote.Description) != text(want.Description) ||
		!slices.Equal(remote.Labels, labels(want.Labels)) && (len(remote.Labels) > 0 || len(labels(want.Labels)) > 0) ||
		!same(want.PreparingTimeoutSeconds, remote.PreparingTimeoutSeconds) ||
		!same(want.DriftDetectionRunLimit, remote.DriftDetectionRunLimit)
}

// managedPool finds the remote pool of a Managed resource, creating it
// when create is set and the resource never recorded one, and brings its
// settings to the resource's.
func (c *Controller) managedPool(ctx context.Context, wp WorkerPool, r *remote, jwt string, owner kernel.WorkerOwner, create bool) (management.WorkerPool, error) {
	got, etag, err := r.managed(ctx, jwt, owner)
	if errors.Is(err, errNotFound) {
		if !create {
			return got, err
		}
		if wp.Status.PoolID != "" {
			return got, fail(ReasonRemotePoolMissing, "The managed pool "+wp.Status.PoolID+" was deleted; this resource does not create a replacement.", err)
		}
		return c.createPool(ctx, wp, r, jwt, owner)
	}
	if err != nil {
		return got, remoteFault("read the managed pool", err)
	}
	if !create {
		return got, nil
	}
	want := settings(wp)
	if drifted(got, want) {
		if err := r.update(ctx, jwt, got.Id, etag, owner, want); err != nil {
			return got, remoteFault("update the managed pool", err)
		}
	}
	return got, nil
}

func (c *Controller) createPool(ctx context.Context, wp WorkerPool, r *remote, jwt string, owner kernel.WorkerOwner) (management.WorkerPool, error) {
	data, err := c.secret(ctx, wp.Namespace, wp.Spec.CredentialsRef.Name, ReasonCredentialsMissing, PrivateKeyKey)
	if err != nil {
		return management.WorkerPool{}, err
	}
	key, err := privateKey(data[PrivateKeyKey])
	if err != nil {
		return management.WorkerPool{}, fail(ReasonCredentialsInvalid, err.Error(), err)
	}
	request, err := csr(key)
	if err != nil {
		return management.WorkerPool{}, fail(ReasonCredentialsInvalid, err.Error(), err)
	}
	s := settings(wp)
	created, err := r.create(ctx, jwt, owner, management.WorkerPoolCreateRequest{
		CertificateSigningRequest: request, Name: s.Name, Space: s.Space, Description: s.Description,
		Labels: s.Labels, PreparingTimeoutSeconds: s.PreparingTimeoutSeconds, DriftDetectionRunLimit: s.DriftDetectionRunLimit,
	})
	if err != nil {
		return created, remoteFault("create the managed pool", err)
	}
	c.log.InfoContext(ctx, "created managed worker pool", "namespace", wp.Namespace, "name", wp.Name, "pool", created.Id)
	return created, nil
}

// host verifies wp's credentials, finds or creates its remote pool and
// binds it to wp. A Managed pool's returned token replaces only the token
// key of its credential Secret.
func (c *Controller) host(ctx context.Context, wp WorkerPool, owner kernel.WorkerOwner) (hosted, error) {
	r := c.remotes.at(wp.Spec.Endpoint)
	data, err := c.secret(ctx, wp.Namespace, wp.Spec.CredentialsRef.Name, ReasonCredentialsMissing, PrivateKeyKey)
	if err != nil {
		return hosted{}, err
	}
	key, err := privateKey(data[PrivateKeyKey])
	if err != nil {
		return hosted{}, fail(ReasonCredentialsInvalid, err.Error(), err)
	}
	token := string(data[TokenKey])
	var id identity
	switch wp.Spec.Mode {
	case Managed:
		jwt, err := c.session(ctx, wp, r)
		if err != nil {
			return hosted{}, err
		}
		got, err := c.managedPool(ctx, wp, r, jwt, owner, true)
		if err != nil {
			return hosted{}, err
		}
		if id, err = newIdentity(got.Config, key, got.Id); err != nil {
			return hosted{}, fail(ReasonCredentialsInvalid, "The pool's current certificate does not certify the referenced private key.", err)
		}
		if token != got.Config {
			if err := c.saveToken(ctx, wp, got.Config); err != nil {
				return hosted{}, err
			}
			token = got.Config
		}
	default:
		if token == "" {
			return hosted{}, fail(ReasonCredentialsMissing, "Existing and Public pools need the token key in their credential Secret.", nil)
		}
		pool := ""
		if wp.Spec.PoolID != nil {
			pool = *wp.Spec.PoolID
		}
		if id, err = newIdentity(token, key, pool); err != nil {
			return hosted{}, fail(ReasonCredentialsInvalid, err.Error(), err)
		}
	}
	proof, err := id.sign(claims{Owner: owner}, c.now())
	if err != nil {
		return hosted{}, fail(ReasonCredentialsInvalid, err.Error(), err)
	}
	if err := r.worker.Bind(ctx, proof); err != nil {
		return hosted{}, remoteFault("bind the pool", err)
	}
	return hosted{remote: r, id: id, token: token, key: data[PrivateKeyKey], owner: owner, public: wp.Spec.Mode == Public}, nil
}

// saveToken patches only the token key of wp's credential Secret, keeping
// its externally managed private key as it is.
func (c *Controller) saveToken(ctx context.Context, wp WorkerPool, token string) error {
	patch, err := jsonPatchToken(token)
	if err != nil {
		return err
	}
	_, err = c.kube.Core.Secrets(wp.Namespace).Patch(ctx, wp.Spec.CredentialsRef.Name, types.MergePatchType, patch, metav1.PatchOptions{FieldValidation: strict})
	if err != nil {
		return fail(ReasonUnavailable, "The pool token cannot be written to its credential Secret.", err)
	}
	return nil
}

// reconcile brings one WorkerPool and its Worker resources to their
// specification, then records what it observed in their status.
func (c *Controller) reconcile(ctx context.Context, wp WorkerPool) error {
	if wp.DeletionTimestamp != nil {
		return c.finalize(ctx, wp)
	}
	if !hasFinalizer(wp.ObjectMeta) {
		if err := c.setFinalizers(ctx, PoolResource, wp.ObjectMeta, append(slices.Clone(wp.Finalizers), Finalizer)); err != nil {
			return err
		}
	}
	before := *wp.Status.DeepCopy()
	status := &wp.Status
	status.ObservedGeneration = wp.Generation
	status.Desired = wp.Spec.Capacity
	owner := wp.Owner(c.settings.ClusterID)
	if c.streams == nil {
		meta.SetStatusCondition(&status.Conditions, condition(ConditionExecutor, false, ReasonExecutorUnavailable, noStreams, wp.Generation))
	} else {
		meta.SetStatusCondition(&status.Conditions, condition(ConditionExecutor, true, ReasonExecutorRunning,
			"Registered slots claim runs and execute each in a Pod of its runner image.", wp.Generation))
	}
	h, err := c.host(ctx, wp, owner)
	if err != nil {
		f := asFault(err)
		meta.SetStatusCondition(&status.Conditions, condition(ConditionBound, false, f.reason, f.message, wp.Generation))
		return errors.Join(err, c.writePoolStatus(ctx, wp, before))
	}
	status.PoolID, status.Owner = h.id.pool, &owner
	meta.SetStatusCondition(&status.Conditions, condition(ConditionBound, true, ReasonBound, "The pool is bound to this resource.", wp.Generation))
	err = c.reconcileWorkers(ctx, &wp, h)
	return errors.Join(err, c.writePoolStatus(ctx, wp, before))
}

func (c *Controller) writePoolStatus(ctx context.Context, wp WorkerPool, before PoolStatus) error {
	if equality.Semantic.DeepEqual(before, wp.Status) {
		return nil
	}
	return c.setStatus(ctx, PoolResource, wp.ObjectMeta, wp.Status)
}

// registration resumes or registers the worker of w's slot.
func (c *Controller) registration(ctx context.Context, h hosted, w Worker) (string, string, error) {
	capabilities := []string{"plan-sanitization-v1"}
	if h.public {
		capabilities = append(capabilities, "public-pool-v1")
	}
	slot := w.Spec.Slot
	jws, err := h.id.sign(claims{
		Owner: h.owner, Slot: &slot, Capabilities: capabilities, Version: c.version, OS: podOS, Arch: c.arch,
		Metadata: map[string]string{"kubernetes.namespace": w.Namespace, "kubernetes.worker": w.Name},
	}, c.now())
	if err != nil {
		return "", "", err
	}
	reg, err := h.remote.worker.Register(ctx, jws)
	if err != nil {
		return "", "", err //nolint:wrapcheck // the client names the call
	}
	return reg.Worker, reg.SessionToken, nil
}

// reconcileWorkers creates the Worker resources of slots 0..capacity-1 and
// keeps their workers registered, and retires the rest. A slot beyond the
// first that the organization's limit refuses gets no resource this pass,
// so a large capacity creates at most one refused slot.
func (c *Controller) reconcileWorkers(ctx context.Context, wp *WorkerPool, h hosted) error {
	existing, err := c.workers(ctx, *wp)
	if err != nil {
		return err
	}
	byName := map[string]Worker{}
	for _, w := range existing {
		byName[w.Name] = w
	}
	var errs []error
	registered, retiring, busy, drained, created := int32(0), int32(0), int32(0), int32(0), 0
	var limited *fault
	for slot := int32(0); slot < wp.Spec.Capacity; slot++ {
		name := SlotName(string(wp.UID), slot)
		w, ok := byName[name]
		delete(byName, name)
		if !ok {
			if limited != nil || created >= c.batch {
				break
			}
			created++
			if w, err = c.createWorker(ctx, newWorker(*wp, h.id.pool, slot)); err != nil {
				errs = append(errs, err)
				break
			}
		}
		if w.DeletionTimestamp != nil {
			retiring++
			errs = append(errs, c.retire(ctx, *wp, &h, w))
			continue
		}
		k, f, err := c.keep(ctx, *wp, h, w, limited == nil)
		errs = append(errs, err)
		if k.registered {
			registered++
		} else if f != nil && f.reason == ReasonLimitReached {
			limited = f
		}
		switch {
		case k.busy:
			busy++
		case k.drained:
			drained++
		}
	}
	for _, w := range byName {
		retiring++
		errs = append(errs, c.retire(ctx, *wp, &h, w))
	}
	status := &wp.Status
	status.Registered, status.Retiring, status.Busy = registered, retiring, busy
	status.Ready = 0
	if c.streams != nil {
		status.Ready = registered - busy - drained
	}
	switch {
	case registered == wp.Spec.Capacity:
		meta.SetStatusCondition(&status.Conditions, condition(ConditionRegistered, true, ReasonRegistered, "Every slot holds a registered worker.", wp.Generation))
	case limited != nil:
		meta.SetStatusCondition(&status.Conditions, condition(ConditionRegistered, false, ReasonLimitReached, limited.message, wp.Generation))
	default:
		meta.SetStatusCondition(
			&status.Conditions,
			condition(ConditionRegistered, false, ReasonRegistering, fmt.Sprintf("%d of %d slots hold a registered worker.", registered, wp.Spec.Capacity), wp.Generation),
		)
	}
	return errors.Join(errs...)
}

// kept is what keep found of a slot: whether its worker is registered,
// whether it executes a run, and whether the installation drains it.
type kept struct {
	registered, busy, drained bool
}

// keep registers w's worker when it has none or its last registration is
// a heartbeat old, unless its slot's launcher runs, which registers and
// polls for itself; starts that launcher, which resumes the assignment
// w's status records; and records the outcome on w's conditions: the
// launcher's executor, whether the installation answers its polls and
// whether it drains the worker. The assignment itself is the launcher's
// to write. A new registration is only attempted when admit is set.
func (c *Controller) keep(ctx context.Context, wp WorkerPool, h hosted, w Worker, admit bool) (kept, *fault, error) {
	before := *w.Status.DeepCopy()
	c.mu.Lock()
	last, seen := c.seen[w.UID]
	live := c.slots[w.UID]
	c.mu.Unlock()
	due := !seen || c.now().Sub(last) >= c.heartbeat
	if live != nil && (!live.alive() || live.l.Status().Worker == "") {
		live = nil
	}
	if w.Status.WorkerID == "" && !admit && live == nil {
		return kept{}, nil, nil
	}
	w.Status.ObservedGeneration = w.Generation
	var f *fault
	var err error
	if live == nil && (w.Status.WorkerID == "" || due) {
		var id string
		id, _, err = c.registration(ctx, h, w)
		if err != nil {
			f = remoteFault("register the worker", err)
			meta.SetStatusCondition(&w.Status.Conditions, condition(ConditionRegistered, false, f.reason, f.message, w.Generation))
			if f.reason == ReasonLimitReached {
				err = nil
			}
		} else {
			c.mu.Lock()
			c.seen[w.UID] = c.now()
			c.mu.Unlock()
			w.Status.WorkerID = id
		}
	}
	var k kept
	if f == nil {
		k.registered = true
		meta.SetStatusCondition(&w.Status.Conditions, condition(ConditionRegistered, true, ReasonRegistered, "The slot's worker is registered.", w.Generation))
		s, serr := c.running(ctx, wp, h, w)
		switch {
		case serr == nil:
			st := s.l.Status()
			if st.Worker != "" {
				w.Status.WorkerID = st.Worker
			}
			k.busy, k.drained = st.Run != "", st.Drained
			meta.SetStatusCondition(&w.Status.Conditions, condition(ConditionExecutor, true, ReasonExecutorRunning, executorRunning, w.Generation))
			c.connection(ctx, s, &w)
		case errors.Is(serr, errNoExecutor):
			meta.SetStatusCondition(&w.Status.Conditions, condition(ConditionExecutor, false, ReasonExecutorUnavailable, noStreams, w.Generation))
		default:
			meta.SetStatusCondition(&w.Status.Conditions, condition(ConditionExecutor, false, ReasonExecutorUnavailable, truncate(serr.Error()), w.Generation))
			err = errors.Join(err, serr)
		}
	} else {
		why := noStreams
		if c.streams != nil {
			why = "The slot's worker is not registered."
		}
		meta.SetStatusCondition(&w.Status.Conditions, condition(ConditionExecutor, false, ReasonExecutorUnavailable, why, w.Generation))
	}
	if !equality.Semantic.DeepEqual(before, w.Status) {
		err = errors.Join(err, c.setWorkerStatus(ctx, w))
	}
	return k, f, err
}

// connection records on w whether the installation answers its slot's
// launcher's polls, and whether it drains the worker, once the launcher
// registered.
func (c *Controller) connection(ctx context.Context, s *slot, w *Worker) {
	st := s.l.Status()
	if st.Worker == "" {
		return
	}
	if err := s.l.Ready(ctx); err != nil {
		meta.SetStatusCondition(&w.Status.Conditions, condition(ConditionConnected, false, ReasonServerUnreachable,
			"The installation answered none of the slot's polls within three poll timeouts; its run, if any, goes on while the launcher retries.", w.Generation))
	} else {
		meta.SetStatusCondition(&w.Status.Conditions, condition(ConditionConnected, true, ReasonPolling, "The installation answers the slot's polls.", w.Generation))
	}
	if st.Drained {
		meta.SetStatusCondition(
			&w.Status.Conditions,
			condition(ConditionDrained, true, ReasonDrained, "The installation drains the worker: it finishes its run and claims no other.", w.Generation),
		)
	} else {
		meta.SetStatusCondition(&w.Status.Conditions, condition(ConditionDrained, false, ReasonAccepting, "The worker claims runs.", w.Generation))
	}
}

// maxMessage bounds a condition's message, which Kubernetes caps at 32768
// bytes.
const maxMessage = 1024

func truncate(s string) string {
	if len(s) <= maxMessage {
		return s
	}
	n := maxMessage
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// retire deletes w, deregisters its worker and then releases its
// finalizer. Deregistration first registers the slot, which resumes its
// worker, so a worker whose ID never reached w's status is deregistered
// too; a slot the limit refuses has no worker to deregister. With no
// hosted pool, as when the remote pool is gone, there is none either.
func (c *Controller) retire(ctx context.Context, wp WorkerPool, h *hosted, w Worker) error {
	if w.DeletionTimestamp == nil {
		if err := c.deleteWorker(ctx, w); err != nil {
			return err
		}
	}
	if err := c.release(ctx, wp, h, w); err != nil {
		return c.blocked(ctx, w, fail(ReasonCleanupBlocked, "The slot's run executes; the slot retires once it ends.", err))
	}
	if !hasFinalizer(w.ObjectMeta) {
		return nil
	}
	if h != nil {
		_, session, err := c.registration(ctx, *h, w)
		switch {
		case err == nil:
			if err := h.remote.worker.Deregister(ctx, session); err != nil {
				return c.blocked(ctx, w, remoteFault("deregister the worker", err))
			}
		case remoteFault("", err).reason != ReasonLimitReached:
			return c.blocked(ctx, w, remoteFault("register the retiring worker", err))
		}
	}
	if err := c.setFinalizers(ctx, WorkerResource, w.ObjectMeta, withoutFinalizer(w.ObjectMeta)); err != nil {
		return err
	}
	c.mu.Lock()
	delete(c.seen, w.UID)
	c.mu.Unlock()
	return nil
}

// blocked records on w why its cleanup cannot finish yet.
func (c *Controller) blocked(ctx context.Context, w Worker, f *fault) error {
	meta.SetStatusCondition(&w.Status.Conditions, condition(ConditionTerminating, false, ReasonCleanupBlocked, f.message, w.Generation))
	return errors.Join(f, c.setWorkerStatus(ctx, w))
}

// finalize retires a deleted pool's workers, then deletes its Managed
// remote pool through the ordinary guarded delete, or releases an Existing
// or Public pool's binding, and only then releases the finalizer. Each
// attempt is bounded by cleanup_timeout; a refusal stays visible on the
// pool and the next pass retries.
func (c *Controller) finalize(ctx context.Context, wp WorkerPool) error {
	if !hasFinalizer(wp.ObjectMeta) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, c.settings.CleanupTimeout)
	defer cancel()
	before := *wp.Status.DeepCopy()
	if err := c.cleanup(ctx, wp); err != nil {
		f := asFault(err)
		meta.SetStatusCondition(&wp.Status.Conditions, condition(ConditionTerminating, false, f.reason, f.message, wp.Generation))
		return errors.Join(err, c.writePoolStatus(ctx, wp, before))
	}
	err := c.setFinalizers(ctx, PoolResource, wp.ObjectMeta, withoutFinalizer(wp.ObjectMeta))
	if err == nil {
		c.log.InfoContext(ctx, "released worker pool", "namespace", wp.Namespace, "name", wp.Name, "pool", wp.Status.PoolID)
	}
	return err
}

func (c *Controller) cleanup(ctx context.Context, wp WorkerPool) error {
	owner := wp.Owner(c.settings.ClusterID)
	workers, err := c.workers(ctx, wp)
	if err != nil {
		return err
	}
	r := c.remotes.at(wp.Spec.Endpoint)
	var jwt string
	var remotePool management.WorkerPool
	exists := wp.Status.PoolID != ""
	if wp.Spec.Mode == Managed {
		if jwt, err = c.session(ctx, wp, r); err != nil {
			return err
		}
		remotePool, err = c.managedPool(ctx, wp, r, jwt, owner, false)
		exists = err == nil
		if err != nil && !errors.Is(err, errNotFound) {
			return err
		}
	}
	if !exists {
		// No remote pool, so no registered worker: only the resources'
		// finalizers remain.
		for _, w := range workers {
			if err := c.retire(ctx, wp, nil, w); err != nil {
				return err
			}
		}
		return nil
	}
	h, err := c.verified(ctx, wp, owner, remotePool)
	if err != nil {
		return err
	}
	var errs []error
	for _, w := range workers {
		errs = append(errs, c.retire(ctx, wp, &h, w))
	}
	if err := errors.Join(errs...); err != nil {
		return fail(ReasonCleanupBlocked, "Workers remain registered.", err)
	}
	if remaining, err := c.workers(ctx, wp); err != nil || len(remaining) > 0 {
		return errors.Join(err, fail(ReasonCleaning, fmt.Sprintf("%d worker resources remain.", len(remaining)), nil))
	}
	if wp.Spec.Mode == Managed {
		if err := r.remove(ctx, jwt, remotePool.Id); err != nil {
			return remoteFault("delete the managed pool", err)
		}
		return nil
	}
	proof, err := h.id.sign(claims{Owner: owner}, c.now())
	if err != nil {
		return fail(ReasonCredentialsInvalid, err.Error(), err)
	}
	if err := r.worker.Release(ctx, proof); err != nil {
		return remoteFault("release the pool", err)
	}
	return nil
}

// verified is the signing identity of a pool being cleaned up, from its
// credential Secret and, for a Managed pool, its current certificate.
func (c *Controller) verified(ctx context.Context, wp WorkerPool, owner kernel.WorkerOwner, remotePool management.WorkerPool) (hosted, error) {
	data, err := c.secret(ctx, wp.Namespace, wp.Spec.CredentialsRef.Name, ReasonCredentialsMissing, PrivateKeyKey)
	if err != nil {
		return hosted{}, err
	}
	key, err := privateKey(data[PrivateKeyKey])
	if err != nil {
		return hosted{}, fail(ReasonCredentialsInvalid, err.Error(), err)
	}
	token, pool := string(data[TokenKey]), wp.Status.PoolID
	if wp.Spec.Mode == Managed {
		token, pool = remotePool.Config, remotePool.Id
	}
	id, err := newIdentity(token, key, pool)
	if err != nil {
		return hosted{}, fail(ReasonCredentialsInvalid, err.Error(), err)
	}
	return hosted{
		remote: c.remotes.at(wp.Spec.Endpoint), id: id, token: token, key: data[PrivateKeyKey], owner: owner,
		public: wp.Spec.Mode == Public,
	}, nil
}

// jsonPatchToken is the merge patch that sets a Secret's token key.
func jsonPatchToken(token string) ([]byte, error) {
	patch, err := json.Marshal(map[string]any{"data": map[string][]byte{TokenKey: []byte(token)}})
	if err != nil {
		return nil, fmt.Errorf("worker controller: token patch: %w", err)
	}
	return patch, nil
}
