package controller

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-jose/go-jose/v4"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/agent/wire/management"
	"github.com/weaveplatform/infraweave/internal/kernel"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
)

// remotePool is a worker pool the fake installation holds.
type remotePool struct {
	id, account, token string
	cert               *x509.Certificate
	management         *kernel.WorkerOwner
	binding            *kernel.WorkerOwner
	settings           management.WorkerPoolUpdateRequest
	deleted            bool
	slots              map[int32]string
}

// installation is a fake infraweave installation: the account API's key
// exchange, account, managed lookup, create, update and delete, and the
// worker API's bind, release, register and deregister. limit bounds the
// registered workers of every pool together, as the organization's
// private-workers limit does. refuse answers an operation, by its name,
// with a status until it is cleared.
type installation struct {
	t   *testing.T
	srv *httptest.Server
	ca  *ecdsa.PrivateKey

	mu         sync.Mutex
	keys       map[string]string // key ID to account
	secrets    map[string]string // key ID to secret
	jwts       map[string]string // token to account
	pools      map[string]*remotePool
	idempotent map[string]string
	sessions   map[string]string // session to worker
	workers    map[string]string // worker to pool
	next       int
	limit      int
	refuse     map[string]int
	calls      map[string]int
	ifMatch    []string
	registered []claims
	wake       chan struct{}                // closed and replaced when a run is queued
	queued     map[string][]wire.Assignment // pool to its runs to assign
	runs       map[string]wire.RunSpec      // run to its sealed specification
	polls      []wire.PollRequest
	reports    []wire.RunReport
}

func newInstallation(t *testing.T) *installation {
	t.Helper()
	ca, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	in := &installation{
		t: t, ca: ca, keys: map[string]string{}, secrets: map[string]string{}, jwts: map[string]string{},
		pools: map[string]*remotePool{}, idempotent: map[string]string{}, sessions: map[string]string{},
		workers: map[string]string{}, limit: 100, refuse: map[string]int{}, calls: map[string]int{},
		queued: map[string][]wire.Assignment{}, runs: map[string]wire.RunSpec{}, wake: make(chan struct{}),
	}
	r := chi.NewRouter()
	r.Post("/api/v1/auth/token", in.exchange)
	r.Get("/api/v1/id", in.id)
	r.Get("/api/v1/worker-pools/managed", in.managed)
	r.Post("/api/v1/worker-pools", in.create)
	r.Put("/api/v1/worker-pools/{id}", in.update)
	r.Delete("/api/v1/worker-pools/{id}", in.remove)
	r.Post("/worker/v1/pools/bind", in.bind)
	r.Post("/worker/v1/pools/release", in.release)
	r.Post("/worker/v1/register", in.register)
	r.Post("/worker/v1/deregister", in.deregister)
	r.Post("/worker/v1/poll", in.poll)
	r.Get("/worker/v1/runs/{run}/spec", in.spec)
	r.Post("/worker/v1/runs/{run}/reports", in.report)
	in.srv = httptest.NewServer(r)
	t.Cleanup(in.srv.Close)
	return in
}

// key adds a management API key of account.
func (in *installation) key(id, account string) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.keys[id], in.secrets[id] = account, "secret-"+id
}

// issue certifies key for a new pool of account, and answers it.
func (in *installation) issue(account string, key *ecdsa.PublicKey) *remotePool {
	in.t.Helper()
	id := kernel.NewULID().String()
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: id}, NotAfter: time.Now().AddDate(1, 0, 0)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key, in.ca)
	if err != nil {
		in.t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		in.t.Fatal(err)
	}
	raw, err := json.Marshal(poolToken{
		Version: tokenVersion, Endpoint: in.srv.URL, Pool: id,
		Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
	})
	if err != nil {
		in.t.Fatal(err)
	}
	p := &remotePool{id: id, account: account, token: base64.StdEncoding.EncodeToString(raw), cert: cert, slots: map[int32]string{}}
	in.pools[id] = p
	return p
}

// pool adds an unmanaged pool of account certifying key, as Existing and
// Public resources name.
func (in *installation) pool(account string, key *ecdsa.PrivateKey) *remotePool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.issue(account, &key.PublicKey)
}

func (in *installation) get(id string) remotePool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return *in.pools[id]
}

func (in *installation) live() []remotePool {
	in.mu.Lock()
	defer in.mu.Unlock()
	var out []remotePool
	for _, p := range in.pools {
		if !p.deleted {
			out = append(out, *p)
		}
	}
	return out
}

func (in *installation) setLimit(n int) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.limit = n
}

func (in *installation) refusing(op string, status int) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if status == 0 {
		delete(in.refuse, op)
		return
	}
	in.refuse[op] = status
}

func (in *installation) count(op string) int {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.calls[op]
}

func (in *installation) registrations() []claims {
	in.mu.Lock()
	defer in.mu.Unlock()
	return append([]claims(nil), in.registered...)
}

// enter counts op and answers its refusal, when one is set; the caller
// holds in.mu while it answers false.
func (in *installation) enter(w http.ResponseWriter, op string) bool {
	in.mu.Lock()
	in.calls[op]++
	if status, ok := in.refuse[op]; ok {
		in.mu.Unlock()
		problem(w, status, "", "refused by the test")
		return false
	}
	return true
}

func problem(w http.ResponseWriter, status int, typ, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": typ, "detail": detail, "status": status})
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (in *installation) caller(r *http.Request) (string, bool) {
	account, ok := in.jwts[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	return account, ok
}

func (in *installation) exchange(w http.ResponseWriter, r *http.Request) {
	if !in.enter(w, "token") {
		return
	}
	defer in.mu.Unlock()
	var req management.ApiKeyUserRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	account, ok := in.keys[req.Id]
	if !ok || in.secrets[req.Id] != req.Secret {
		problem(w, http.StatusUnauthorized, "", "unknown key")
		return
	}
	in.next++
	jwt := "jwt-" + strconv.Itoa(in.next)
	in.jwts[jwt] = account
	reply(w, http.StatusOK, map[string]any{"jwt": jwt, "validUntil": time.Now().Add(time.Hour).Unix()})
}

func (in *installation) id(w http.ResponseWriter, r *http.Request) {
	if !in.enter(w, "id") {
		return
	}
	defer in.mu.Unlock()
	account, ok := in.caller(r)
	if !ok {
		problem(w, http.StatusUnauthorized, "", "unknown token")
		return
	}
	reply(w, http.StatusOK, account)
}

func view(p *remotePool) management.WorkerPool {
	s := p.settings
	out := management.WorkerPool{
		Id: p.id, Config: p.token, Name: s.Name, Description: s.Description,
		PreparingTimeoutSeconds: s.PreparingTimeoutSeconds, DriftDetectionRunLimit: s.DriftDetectionRunLimit, Labels: []string{},
	}
	if s.Space != nil {
		out.Space = *s.Space
	}
	if s.Labels != nil {
		out.Labels = *s.Labels
	}
	return out
}

func (in *installation) managed(w http.ResponseWriter, r *http.Request) {
	if !in.enter(w, "managed") {
		return
	}
	defer in.mu.Unlock()
	account, ok := in.caller(r)
	if !ok {
		problem(w, http.StatusUnauthorized, "", "unknown token")
		return
	}
	q := r.URL.Query()
	owner := kernel.WorkerOwner{ClusterID: q.Get("clusterID"), Namespace: q.Get("namespace"), ResourceUID: q.Get("resourceUID")}
	for _, p := range in.pools {
		if p.account == account && !p.deleted && p.management != nil && *p.management == owner {
			w.Header().Set("ETag", fmt.Sprintf(`"%s-%d"`, p.id, in.calls["update"]))
			reply(w, http.StatusOK, view(p))
			return
		}
	}
	problem(w, http.StatusNotFound, "", "no managed pool")
}

func (in *installation) create(w http.ResponseWriter, r *http.Request) {
	if !in.enter(w, "create") {
		return
	}
	defer in.mu.Unlock()
	account, ok := in.caller(r)
	if !ok {
		problem(w, http.StatusUnauthorized, "", "unknown token")
		return
	}
	var req management.WorkerPoolCreateRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	key := r.Header.Get("Idempotency-Key")
	if id, ok := in.idempotent[key]; ok {
		reply(w, http.StatusCreated, view(in.pools[id]))
		return
	}
	der, err := base64.StdEncoding.DecodeString(req.CertificateSigningRequest)
	if err != nil {
		problem(w, http.StatusBadRequest, "", "csr")
		return
	}
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil || csr.CheckSignature() != nil || req.Management == nil || key == "" {
		problem(w, http.StatusBadRequest, "", "csr")
		return
	}
	pub, _ := csr.PublicKey.(*ecdsa.PublicKey)
	p := in.issue(account, pub)
	owner := kernel.WorkerOwner{ClusterID: req.Management.ClusterID, Namespace: req.Management.Namespace, ResourceUID: req.Management.ResourceUID}
	p.management, p.binding = &owner, &owner
	p.settings = management.WorkerPoolUpdateRequest{
		Name: req.Name, Space: req.Space, Description: req.Description, Labels: req.Labels,
		PreparingTimeoutSeconds: req.PreparingTimeoutSeconds, DriftDetectionRunLimit: req.DriftDetectionRunLimit,
	}
	in.idempotent[key] = p.id
	reply(w, http.StatusCreated, view(p))
}

func (in *installation) update(w http.ResponseWriter, r *http.Request) {
	if !in.enter(w, "update") {
		return
	}
	defer in.mu.Unlock()
	var req management.WorkerPoolUpdateRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	p := in.pools[chi.URLParam(r, "id")]
	in.ifMatch = append(in.ifMatch, r.Header.Get("If-Match"))
	req.Management = nil
	p.settings = req
	reply(w, http.StatusOK, view(p))
}

func (in *installation) remove(w http.ResponseWriter, r *http.Request) {
	if !in.enter(w, "delete") {
		return
	}
	defer in.mu.Unlock()
	p, ok := in.pools[chi.URLParam(r, "id")]
	if !ok || p.deleted {
		problem(w, http.StatusNotFound, "", "gone")
		return
	}
	if len(p.slots) > 0 {
		problem(w, http.StatusConflict, "", "workers remain")
		return
	}
	p.deleted = true
	reply(w, http.StatusOK, view(p))
}

// proof verifies a signed proof against its pool's certificate and
// answers its claims and pool; the caller holds in.mu.
func (in *installation) proof(w http.ResponseWriter, raw string) (claims, *remotePool, bool) {
	obj, err := jose.ParseSignedCompact(raw, []jose.SignatureAlgorithm{jose.ES256})
	if err != nil {
		problem(w, http.StatusUnauthorized, "", "proof")
		return claims{}, nil, false
	}
	var c claims
	if err := json.Unmarshal(obj.UnsafePayloadWithoutVerification(), &c); err != nil {
		problem(w, http.StatusUnauthorized, "", "proof")
		return claims{}, nil, false
	}
	p, ok := in.pools[c.Pool]
	if !ok || p.deleted {
		problem(w, http.StatusUnauthorized, "", "pool")
		return claims{}, nil, false
	}
	if _, err := obj.Verify(p.cert.PublicKey); err != nil {
		problem(w, http.StatusUnauthorized, "", "signature")
		return claims{}, nil, false
	}
	return c, p, true
}

func (in *installation) ownership(w http.ResponseWriter, r *http.Request, op string) (claims, *remotePool, bool) {
	if !in.enter(w, op) {
		return claims{}, nil, false
	}
	var req struct {
		Proof        string `json:"proof"`
		Registration string `json:"registration"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	c, p, ok := in.proof(w, req.Proof+req.Registration)
	if !ok {
		in.mu.Unlock()
	}
	return c, p, ok
}

func (in *installation) bind(w http.ResponseWriter, r *http.Request) {
	c, p, ok := in.ownership(w, r, "bind")
	if !ok {
		return
	}
	defer in.mu.Unlock()
	if p.binding != nil && *p.binding != c.Owner || p.management != nil && *p.management != c.Owner {
		problem(w, http.StatusConflict, "urn:infraweave:problem:invalid-state", "bound to another owner")
		return
	}
	owner := c.Owner
	p.binding = &owner
	w.WriteHeader(http.StatusNoContent)
}

func (in *installation) release(w http.ResponseWriter, r *http.Request) {
	c, p, ok := in.ownership(w, r, "release")
	if !ok {
		return
	}
	defer in.mu.Unlock()
	if p.binding == nil || *p.binding != c.Owner || len(p.slots) > 0 {
		problem(w, http.StatusConflict, "urn:infraweave:problem:invalid-state", "not releasable")
		return
	}
	p.binding = nil
	w.WriteHeader(http.StatusNoContent)
}

func (in *installation) register(w http.ResponseWriter, r *http.Request) {
	c, p, ok := in.ownership(w, r, "register")
	if !ok {
		return
	}
	defer in.mu.Unlock()
	if p.binding == nil || *p.binding != c.Owner || c.Slot == nil {
		problem(w, http.StatusConflict, "urn:infraweave:problem:invalid-state", "not bound to this owner")
		return
	}
	in.registered = append(in.registered, c)
	worker, ok := p.slots[*c.Slot]
	if !ok {
		if len(in.workers) >= in.limit {
			problem(w, http.StatusForbidden, "urn:infraweave:problem:limit-reached", "private-workers")
			return
		}
		worker = kernel.NewULID().String()
		p.slots[*c.Slot] = worker
		in.workers[worker] = p.id
	}
	in.next++
	session := "session-" + strconv.Itoa(in.next)
	in.sessions[session] = worker
	reply(w, http.StatusOK, map[string]any{"worker": worker, "sessionToken": session, "sessionExpiresAt": time.Now().Add(time.Hour).Unix()})
}

func (in *installation) deregister(w http.ResponseWriter, r *http.Request) {
	if !in.enter(w, "deregister") {
		return
	}
	defer in.mu.Unlock()
	worker, ok := in.sessions[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	if !ok {
		problem(w, http.StatusUnauthorized, "", "session")
		return
	}
	p := in.pools[in.workers[worker]]
	for slot, id := range p.slots {
		if id == worker {
			delete(p.slots, slot)
		}
	}
	delete(in.workers, worker)
	w.WriteHeader(http.StatusNoContent)
}

// assign queues run of pool for the next idle poll of its workers, its
// secrets sealed to the pool's key as the server seals them.
func (in *installation) assign(pool string, a wire.Assignment, spec wire.RunSpec, secrets wire.RunSpecSecrets) {
	in.t.Helper()
	in.mu.Lock()
	defer in.mu.Unlock()
	pub, err := in.pools[pool].cert.PublicKey.(*ecdsa.PublicKey).ECDH()
	if err != nil {
		in.t.Fatal(err)
	}
	plain, err := json.Marshal(secrets)
	if err != nil {
		in.t.Fatal(err)
	}
	if spec.Sealed, err = seal.SealEnvelope(pub, a.Run, a.StateVersion, plain); err != nil {
		in.t.Fatal(err)
	}
	in.runs[a.Run] = spec
	in.queued[pool] = append(in.queued[pool], a)
	close(in.wake)
	in.wake = make(chan struct{})
}

func (in *installation) pollRequests() []wire.PollRequest {
	in.mu.Lock()
	defer in.mu.Unlock()
	return append([]wire.PollRequest(nil), in.polls...)
}

// poll answers an idle worker a queued run of its pool, holding the poll
// until there is one or its wait passes.
func (in *installation) poll(w http.ResponseWriter, r *http.Request) {
	if !in.enter(w, "poll") {
		return
	}
	worker, ok := in.sessions[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	pool := in.workers[worker]
	var req wire.PollRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	in.polls = append(in.polls, req)
	in.mu.Unlock()
	if !ok || pool == "" {
		problem(w, http.StatusUnauthorized, "", "session")
		return
	}
	wait := time.Second
	if req.Wait != nil {
		wait = time.Duration(*req.Wait) * time.Second
	}
	hold := time.NewTimer(wait)
	defer hold.Stop()
	for {
		in.mu.Lock()
		if q := in.queued[pool]; !req.Busy && len(q) > 0 {
			in.queued[pool] = q[1:]
			in.mu.Unlock()
			reply(w, http.StatusOK, wire.PollResult{Assignment: &q[0]})
			return
		}
		wake := in.wake
		in.mu.Unlock()
		select {
		case <-wake:
		case <-hold.C:
			reply(w, http.StatusOK, wire.PollResult{})
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (in *installation) spec(w http.ResponseWriter, r *http.Request) {
	in.mu.Lock()
	defer in.mu.Unlock()
	spec, ok := in.runs[chi.URLParam(r, "run")]
	if !ok {
		problem(w, http.StatusNotFound, "", "run")
		return
	}
	reply(w, http.StatusOK, spec)
}

func (in *installation) report(w http.ResponseWriter, r *http.Request) {
	in.mu.Lock()
	defer in.mu.Unlock()
	var rep wire.RunReport
	_ = json.NewDecoder(r.Body).Decode(&rep)
	in.reports = append(in.reports, rep)
	reply(w, http.StatusOK, wire.NextStep{})
}
