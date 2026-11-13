package agenttest

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-jose/go-jose/v4"

	"github.com/weaveplatform/infraweave/internal/agent/wire"
	"github.com/weaveplatform/infraweave/internal/platform/seal"
)

// Endpoint is the server's public URL a Pool's token names. Nothing
// listens there: Client serves its requests in process.
const Endpoint = "http://agenttest.invalid"

// Pool is a worker pool's credentials: its ID, its pool token, and its
// private key in PEM, as a launcher takes them.
type Pool struct {
	ID    string
	Token string
	Key   []byte
	cert  *x509.Certificate
	key   *ecdsa.PrivateKey
}

// NewPool makes a pool with a fresh P-256 key and a certificate for it,
// whose common name is the pool's ID, as the server issues.
func NewPool() (Pool, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Pool{}, fmt.Errorf("agenttest: pool key: %w", err)
	}
	id := "pool-" + strings.ToLower(rand.Text())
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: id},
		NotAfter: time.Now().AddDate(10, 0, 0),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return Pool{}, fmt.Errorf("agenttest: pool certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return Pool{}, fmt.Errorf("agenttest: pool certificate: %w", err)
	}
	token, err := json.Marshal(map[string]any{
		"version": 1, "endpoint": Endpoint, "pool": id,
		"certificate": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
	})
	if err != nil {
		return Pool{}, fmt.Errorf("agenttest: pool token: %w", err)
	}
	sec1, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return Pool{}, fmt.Errorf("agenttest: pool key: %w", err)
	}
	return Pool{
		ID: id, Token: base64.StdEncoding.EncodeToString(token),
		Key:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: sec1}),
		cert: cert, key: key,
	}, nil
}

// Registration is a registration the server took: its claims, the worker
// it answered and whether that worker was the one the claims named.
type Registration struct {
	Claims  map[string]any
	Worker  string
	Resumed bool
}

// Poll is a poll the server took: when, the worker's session token and
// its request.
type Poll struct {
	At      time.Time
	Session string
	Request wire.PollRequest
}

// Report is a report the server took for a run, and how many of the
// run's log chunks it had indexed when it took it.
type Report struct {
	Run     string
	Token   string
	Report  wire.RunReport
	Indexed int
}

// Chunk is a log chunk the server indexed: its run, state version and
// sequence number, its first and last lines' times, and its bytes.
type Chunk struct {
	Run          string
	StateVersion int64
	Seq          int64
	FirstAt      int64
	LastAt       int64
	Body         []byte
}

// object is an upload the server signed: its declaration, its bytes once
// PUT, and whether a commit recorded it.
type object struct {
	decl     wire.Upload
	body     []byte
	uploaded bool
	recorded bool
}

// ContentType is the header each signed upload names, which its PUT must
// carry.
const ContentType = "application/octet-stream"

// Run is a run the server assigns: its assignment, and the specification
// and secrets its spec call answers, sealed for the assignment's state
// version.
type Run struct {
	Assignment wire.Assignment
	Spec       wire.RunSpec
	Secrets    wire.RunSpecSecrets
}

// Server is a fake worker API of one pool, served in process. It
// registers and resumes workers, holds polls until the test gives it
// something to answer, assigns runs, answers their specifications and
// reports, and records every call. Its fields that make it refuse are set
// through its methods.
type Server struct {
	pool Pool
	h    http.Handler

	mu            sync.Mutex
	wake          chan struct{}
	sessions      map[string]string
	workers       map[string]bool
	next          int
	answers       []wire.PollResult
	runs          map[string]Run
	registrations []Registration
	polls         []Poll
	reports       []Report
	objects       map[string]*object
	chunks        []Chunk
	deregistered  []string
	refuse        map[string]int
	lost          bool
}

// NewServer is a fake worker API for pool.
func NewServer(pool Pool) *Server {
	s := &Server{
		pool: pool, wake: make(chan struct{}), sessions: map[string]string{},
		workers: map[string]bool{}, runs: map[string]Run{}, refuse: map[string]int{},
		objects: map[string]*object{},
	}
	r := chi.NewRouter()
	r.Route("/worker/v1", func(r chi.Router) {
		r.Post("/register", s.register)
		r.Post("/poll", s.poll)
		r.Post("/deregister", s.deregister)
		r.Get("/runs/{run}/spec", s.spec)
		r.Post("/runs/{run}/reports", s.report)
		r.Post("/runs/{run}/upload-urls", s.uploadURLs)
		r.Post("/runs/{run}/log-chunks", s.logChunks)
	})
	r.Put("/store/*", s.put)
	s.h = r
	return s
}

// Client is an HTTP client whose requests the server serves in process,
// so a test in a synctest bubble holds polls on its fake clock.
func (s *Server) Client() *http.Client { return &http.Client{Transport: inProcess{s.h}} }

type inProcess struct{ h http.Handler }

func (t inProcess) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	t.h.ServeHTTP(rec, req)
	if err := req.Context().Err(); err != nil {
		return nil, fmt.Errorf("agenttest: %w", err)
	}
	return rec.Result(), nil
}

// Refuse makes the server answer the next n calls of op (register, poll,
// deregister, spec, reports, upload-urls, put or log-chunks) with status;
// n of 0 takes op's calls again.
func (s *Server) Refuse(op string, status, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n <= 0 {
		delete(s.refuse, op)
		return
	}
	s.refuse[op] = n<<16 | status
}

// refused answers the status of a refusal of op, and counts it down.
func (s *Server) refused(op string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.refuse[op]
	if !ok {
		return 0
	}
	if n := v>>16 - 1; n > 0 {
		s.refuse[op] = n<<16 | v&0xffff
	} else {
		delete(s.refuse, op)
	}
	return v & 0xffff
}

// Lose makes the server forget every worker and session, as when it
// reaped a worker whose run it then lost.
func (s *Server) Lose() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lost = true
	clear(s.sessions)
	clear(s.workers)
}

// Renew gives the worker of session a new session token, and queues the
// poll result that hands it over.
func (s *Server) Renew(session string) string {
	s.mu.Lock()
	renewed := session + "-renewed"
	s.sessions[renewed] = s.sessions[session]
	s.mu.Unlock()
	s.Answer(wire.PollResult{SessionToken: &renewed})
	return renewed
}

// Answer queues a poll result, which the next poll answers.
func (s *Server) Answer(r wire.PollResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answers = append(s.answers, r)
	close(s.wake)
	s.wake = make(chan struct{})
}

// Assign queues r's assignment, and seals its secrets for the pool.
func (s *Server) Assign(r Run) error {
	plain, err := json.Marshal(r.Secrets) //nolint:gosec // sealed next, as the server seals them
	if err != nil {
		return fmt.Errorf("agenttest: %w", err)
	}
	pub, err := s.pool.key.PublicKey.ECDH()
	if err != nil {
		return fmt.Errorf("agenttest: %w", err)
	}
	r.Spec.Sealed, err = seal.SealEnvelope(pub, r.Assignment.Run, r.Assignment.StateVersion, plain)
	if err != nil {
		return fmt.Errorf("agenttest: %w", err)
	}
	s.mu.Lock()
	s.runs[r.Assignment.Run] = r
	s.mu.Unlock()
	s.Answer(wire.PollResult{Assignment: &r.Assignment})
	return nil
}

// Registrations, Polls, Reports, Chunks and Deregistered are the calls
// and log chunks the server took, in order.
func (s *Server) Registrations() []Registration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Registration(nil), s.registrations...)
}

func (s *Server) Polls() []Poll {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Poll(nil), s.polls...)
}

func (s *Server) Reports() []Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Report(nil), s.reports...)
}

func (s *Server) Chunks() []Chunk {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Chunk(nil), s.chunks...)
}

func (s *Server) Deregistered() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.deregistered...)
}

func problem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	code := int32(status) //nolint:gosec // an HTTP status
	_ = json.NewEncoder(w).Encode(wire.Problem{
		Status: code,
		Title:  http.StatusText(status),
		Detail: &detail,
	})
}

func answer(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// register checks the JWS against the pool's certificate, as the server
// does, and registers or resumes the worker it names.
func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if st := s.refused("register"); st != 0 {
		problem(w, st, "the registration is refused")
		return
	}
	var req wire.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem(w, http.StatusBadRequest, err.Error())
		return
	}
	jws, err := jose.ParseSignedCompact(req.Registration, []jose.SignatureAlgorithm{jose.ES256})
	if err != nil {
		problem(w, http.StatusUnauthorized, err.Error())
		return
	}
	certs, err := jws.Signatures[0].Protected.Certificates(x509.VerifyOptions{
		Roots: certPool(s.pool.cert), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	if err != nil || !certs[0][0].Equal(s.pool.cert) {
		problem(w, http.StatusUnauthorized, fmt.Sprintf("x5c: %v", err))
		return
	}
	payload, err := jws.Verify(&s.pool.key.PublicKey)
	if err != nil {
		problem(w, http.StatusUnauthorized, err.Error())
		return
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil || claims["pool"] != s.pool.ID {
		problem(w, http.StatusUnauthorized, "not this pool's registration")
		return
	}
	named, _ := claims["worker"].(string)
	s.mu.Lock()
	reg := Registration{Claims: claims, Worker: named, Resumed: s.workers[named]}
	if !reg.Resumed {
		s.next++
		reg.Worker = "worker-" + strconv.Itoa(s.next)
		s.workers[reg.Worker] = true
	}
	session := "session-" + strconv.Itoa(len(s.registrations)+1)
	s.sessions[session] = reg.Worker
	s.registrations = append(s.registrations, reg)
	s.mu.Unlock()
	answer(w, wire.Registration{
		Worker:           reg.Worker,
		SessionToken:     session,
		SessionExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
}

func certPool(c *x509.Certificate) *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(c)
	return p
}

// worker is the worker whose session r carries.
func (s *Server) worker(r *http.Request) (string, string, bool) {
	session := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.sessions[session]
	return w, session, ok
}

// poll answers the oldest queued result, holding the poll until one is
// queued or its wait passes.
func (s *Server) poll(w http.ResponseWriter, r *http.Request) {
	if st := s.refused("poll"); st != 0 {
		problem(w, st, "the poll is refused")
		return
	}
	_, session, ok := s.worker(r)
	if !ok {
		problem(w, http.StatusUnauthorized, "the session is refused")
		return
	}
	var req wire.PollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem(w, http.StatusBadRequest, err.Error())
		return
	}
	s.mu.Lock()
	s.polls = append(s.polls, Poll{At: time.Now(), Session: session, Request: req})
	s.mu.Unlock()
	var wait int32
	if req.Wait != nil {
		wait = *req.Wait
	}
	hold := time.NewTimer(time.Duration(wait) * time.Second)
	defer hold.Stop()
	for {
		s.mu.Lock()
		if len(s.answers) > 0 {
			res := s.answers[0]
			s.answers = s.answers[1:]
			s.mu.Unlock()
			answer(w, res)
			return
		}
		wake := s.wake
		s.mu.Unlock()
		select {
		case <-wake:
		case <-hold.C:
			answer(w, wire.PollResult{})
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) deregister(w http.ResponseWriter, r *http.Request) {
	if st := s.refused("deregister"); st != 0 {
		problem(w, st, "the deregistration is refused")
		return
	}
	worker, session, ok := s.worker(r)
	if !ok {
		problem(w, http.StatusUnauthorized, "the session is refused")
		return
	}
	s.mu.Lock()
	delete(s.sessions, session)
	delete(s.workers, worker)
	s.deregistered = append(s.deregistered, worker)
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// run is the assigned run r names, when r carries its run token.
func (s *Server) run(r *http.Request) (Run, string, bool) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[chi.URLParam(r, "run")]
	return run, token, ok && token == run.Assignment.RunToken
}

func (s *Server) spec(w http.ResponseWriter, r *http.Request) {
	if st := s.refused("spec"); st != 0 {
		problem(w, st, "the run is not on a worker")
		return
	}
	run, _, ok := s.run(r)
	if !ok {
		problem(w, http.StatusUnauthorized, "the run token is refused")
		return
	}
	answer(w, run.Spec)
}

// report records a report and answers the step it leads to: a started
// report executes its state, and any other ends the run.
func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	if st := s.refused("reports"); st != 0 {
		problem(w, st, "the run moved on")
		return
	}
	run, token, ok := s.run(r)
	if !ok {
		problem(w, http.StatusUnauthorized, "the run token is refused")
		return
	}
	var rep wire.RunReport
	if err := json.NewDecoder(r.Body).Decode(&rep); err != nil {
		problem(w, http.StatusBadRequest, err.Error())
		return
	}
	s.mu.Lock()
	indexed := 0
	for _, c := range s.chunks {
		if c.Run == run.Assignment.Run {
			indexed++
		}
	}
	s.reports = append(s.reports, Report{
		Run: run.Assignment.Run, Token: token, Report: rep, Indexed: indexed,
	})
	s.mu.Unlock()
	step := wire.NextStep{
		Action:       wire.Execute,
		State:        string(rep.State),
		StateVersion: rep.StateVersion + 1,
	}
	if rep.Event != wire.Started {
		step.Action, step.State = wire.Exit, "FAILED"
	}
	answer(w, step)
}

// objectKey is where the upload named name of run's state version is
// stored, by its kind.
func objectKey(kind wire.UploadKind, run string, version int64, name string) string {
	return fmt.Sprintf("%s/%s/%d/%s", kind, run, version, name)
}

// uploadURLs signs a PUT for each declared upload, as the server does: a
// declaration repeated as it was signs the same object again, and one
// that differs, or names an object a commit recorded, is refused with
// 409.
func (s *Server) uploadURLs(w http.ResponseWriter, r *http.Request) {
	if st := s.refused("upload-urls"); st != 0 {
		problem(w, st, "the uploads are refused")
		return
	}
	run, _, ok := s.run(r)
	if !ok {
		problem(w, http.StatusUnauthorized, "the run token is refused")
		return
	}
	var req wire.UploadURLsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem(w, http.StatusBadRequest, err.Error())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := wire.UploadURLs{Uploads: make([]wire.SignedUpload, 0, len(req.Uploads))}
	for _, u := range req.Uploads {
		key := objectKey(u.Kind, run.Assignment.Run, req.StateVersion, u.Name)
		if o, ok := s.objects[key]; ok && (o.recorded || o.decl != u) {
			problem(w, http.StatusConflict, "the upload's name is taken")
			return
		}
		if _, ok := s.objects[key]; !ok {
			s.objects[key] = &object{decl: u}
		}
		out.Uploads = append(out.Uploads, wire.SignedUpload{
			Kind: u.Kind, Name: u.Name, Key: key, Url: Endpoint + "/store/" + key,
			Headers: map[string]string{"Content-Type": ContentType},
		})
	}
	answer(w, out)
}

// put takes a signed upload's bytes when they are its declaration's, as a
// store whose signature binds the size and SHA-256 does.
func (s *Server) put(w http.ResponseWriter, r *http.Request) {
	if st := s.refused("put"); st != 0 {
		w.WriteHeader(st)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	sum := sha256.Sum256(body)
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objects[chi.URLParam(r, "*")]
	switch {
	case !ok || r.Header.Get("Content-Type") != ContentType ||
		r.Header.Get("Authorization") != "":
		w.WriteHeader(http.StatusForbidden)
	case int64(len(body)) != o.decl.Size || hex.EncodeToString(sum[:]) != o.decl.Sha256:
		w.WriteHeader(http.StatusBadRequest)
	default:
		o.body, o.uploaded = bytes.Clone(body), true
		w.WriteHeader(http.StatusOK)
	}
}

// logChunks indexes each uploaded chunk once, as the server does; a chunk
// not uploaded is refused with 422.
func (s *Server) logChunks(w http.ResponseWriter, r *http.Request) {
	if st := s.refused("log-chunks"); st != 0 {
		problem(w, st, "the chunks are refused")
		return
	}
	run, _, ok := s.run(r)
	if !ok {
		problem(w, http.StatusUnauthorized, "the run token is refused")
		return
	}
	var req wire.LogChunksRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		problem(w, http.StatusBadRequest, err.Error())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range req.Chunks {
		key := objectKey(wire.UploadKindLogChunk, run.Assignment.Run, req.StateVersion,
			fmt.Sprint(c.Seq))
		o, ok := s.objects[key]
		switch {
		case ok && o.recorded:
			continue
		case !ok || !o.uploaded:
			problem(w, http.StatusUnprocessableEntity, "a chunk is not uploaded")
			return
		}
		o.recorded = true
		s.chunks = append(s.chunks, Chunk{
			Run: run.Assignment.Run, StateVersion: req.StateVersion, Seq: c.Seq,
			FirstAt: c.FirstAt, LastAt: c.LastAt, Body: o.body,
		})
	}
	w.WriteHeader(http.StatusNoContent)
}
