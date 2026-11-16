package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/weaveplatform/infraweave/internal/agent/executor/docker"
)

// EnvStress puts a run of the suite under stress: a comma-separated list
// of refuse=<share>, latency=<duration> and cpus=<CPUs>, each optional,
// such as refuse=0.2,latency=500ms,cpus=0.5.
//
// ref: decision 0016
const EnvStress = "INFRAWEAVE_ACCEPT_STRESS"

// ErrStress is returned for an EnvStress value that names an unknown
// stress or a value out of its range.
var ErrStress = errors.New(EnvStress)

// Stress is what a busy or badly synchronised host does to a run, which a
// stressed run of the suite reproduces: the object store refusing signed
// uploads, as a store whose clock differs from the signer's does, slow
// store answers, and run containers with little CPU. The zero Stress is a
// quiet host. No step's assertion or Eventually budget changes under
// stress, so a budget too short for a busy host fails as a finding.
type Stress struct {
	// Refuse is the share, 0 to 1, of signed uploads an @objectstore
	// scenario's store relay refuses.
	Refuse float64
	// Latency is how long the store relay holds each request before it
	// passes it on or refuses it.
	Latency time.Duration
	// CPUs caps each run container, and sets GOMAXPROCS for each process
	// mode a scenario starts to CPUs rounded up; 0 caps nothing.
	CPUs float64
}

// ParseStress is the Stress s, an EnvStress value, names.
func ParseStress(s string) (Stress, error) {
	var st Stress
	for item := range strings.SplitSeq(s, ",") {
		if item = strings.TrimSpace(item); item == "" {
			continue
		}
		key, value, _ := strings.Cut(item, "=")
		var err error
		switch key {
		case "refuse":
			st.Refuse, err = strconv.ParseFloat(value, 64)
			if err == nil && (st.Refuse < 0 || st.Refuse > 1) {
				err = errors.New("not a share from 0 to 1")
			}
		case "latency":
			st.Latency, err = time.ParseDuration(value)
			if err == nil && st.Latency < 0 {
				err = errors.New("negative")
			}
		case "cpus":
			st.CPUs, err = strconv.ParseFloat(value, 64)
			if err == nil && (st.CPUs < 0 || math.IsInf(st.CPUs, 0)) {
				err = errors.New("not a CPU count")
			}
		default:
			err = errors.New("no such stress")
		}
		if err != nil {
			return Stress{}, fmt.Errorf("%w: %s: %w", ErrStress, item, err)
		}
	}
	return st, nil
}

// store reports whether s needs a store relay.
func (s Stress) store() bool { return s.Refuse > 0 || s.Latency > 0 }

// procs is the GOMAXPROCS of a process mode under s, or "" for none.
func (s Stress) procs() string {
	if s.CPUs == 0 {
		return ""
	}
	return strconv.Itoa(max(1, int(math.Ceil(s.CPUs))))
}

// skewed is the body of the 403 an S3 store answers a request signed
// further from its clock than it allows.
const skewed = `<?xml version="1.0" encoding="UTF-8"?>
<Error><Code>RequestTimeTooSkewed</Code><Message>The difference between the request time and the current time is too large.</Message></Error>`

// maxDrain bounds the body of a refused upload the store relay reads
// before it answers, so a runner writing it sees the 403, not a reset.
const maxDrain = 16 << 20

// StoreRelay is an @objectstore scenario's object store as its servers
// and its run containers reach it under stress: a reverse proxy on
// loopback, which the URLs the servers sign name, recording each call.
// It holds each request for the stress's latency, and refuses its share
// of signed requests, each one whose query carries the signature, with
// the 403 RequestTimeTooSkewed a skewed store answers: of uploads, and,
// counted apart, of the launcher's reads, a saved workspace or a local
// preview's archive. A log or a profile a step reads through a URL the
// API signed for its client passes, since only that client could ask
// for a URL signed afresh. Refusals are spread evenly, so a share of 0.2
// refuses every fifth upload from the fifth, and every fifth read from
// the first: a scenario's launcher reads one or two objects, which a
// spread from the fifth would never reach, while its first upload may be
// a step's own, which nothing signs afresh. It passes each request on
// with its Host, which the request's signature covers.
type StoreRelay struct {
	// URL is the relay's base URL; Port is its port.
	URL, Port string
	srv       *http.Server
	proxy     *httputil.ReverseProxy
	stress    Stress
	mu        sync.Mutex
	refusals  Refusals
	calls     []Call
}

// Refusals counts the signed requests a store relay may refuse, uploads
// and the launcher's reads, and how many of each it refused. A stressed
// run that passes leaves no other trace that its refusals happened.
type Refusals struct {
	Uploads        int `json:"uploads"`
	UploadsRefused int `json:"uploadsRefused"`
	Reads          int `json:"reads"`
	ReadsRefused   int `json:"readsRefused"`
}

func (a Refusals) add(b Refusals) Refusals {
	return Refusals{
		Uploads: a.Uploads + b.Uploads, UploadsRefused: a.UploadsRefused + b.UploadsRefused,
		Reads: a.Reads + b.Reads, ReadsRefused: a.ReadsRefused + b.ReadsRefused,
	}
}

// StartStoreRelay starts a relay under stress to the object store at
// target, a base URL.
func StartStoreRelay(ctx context.Context, target string, stress Stress) (*StoreRelay, error) {
	to, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("store relay: %w", err)
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("store relay: %w", err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	r := &StoreRelay{URL: "http://" + ln.Addr().String(), Port: port, stress: stress}
	r.proxy = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(to)
			pr.Out.Host = pr.In.Host
		},
		ModifyResponse: func(resp *http.Response) error {
			r.record(resp.Request.Method, resp.Request.URL.Path, resp.StatusCode)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, _ error) {
			r.record(req.Method, req.URL.Path, http.StatusBadGateway)
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	r.srv = &http.Server{Handler: r, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = r.srv.Serve(ln) }()
	return r, nil
}

// ServeHTTP holds req for the latency, then refuses it or passes it on.
func (r *StoreRelay) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if r.stress.Latency > 0 {
		t := time.NewTimer(r.stress.Latency)
		select {
		case <-t.C:
		case <-req.Context().Done():
			t.Stop()
			return
		}
	}
	if r.refuses(req) {
		_, _ = io.Copy(io.Discard, io.LimitReader(req.Body, maxDrain))
		r.record(req.Method, req.URL.Path, http.StatusForbidden)
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, skewed)
		return
	}
	r.proxy.ServeHTTP(w, req)
}

// launcherReads are the stores a launcher reads through a pre-signed URL:
// a saved workspace and a local preview's uploaded archive.
var launcherReads = []string{"workspaces", "uploads"}

// refuses reports whether the relay refuses req: a signed upload or a
// signed read of launcherReads is the n-th of its kind, and the n-th is
// refused when the share's multiples pass a whole number at it, rounded
// down for an upload and up for a read, so reads are refused from the
// first. A path is /<bucket>/<store>/<key>.
func (r *StoreRelay) refuses(req *http.Request) bool {
	if !req.URL.Query().Has("X-Amz-Signature") {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var seen, refused *int
	round := math.Floor
	switch store := strings.SplitN(req.URL.Path, "/", 4); {
	case req.Method == http.MethodPut:
		seen, refused = &r.refusals.Uploads, &r.refusals.UploadsRefused
	case req.Method == http.MethodGet && len(store) == 4 && slices.Contains(launcherReads, store[2]):
		seen, refused, round = &r.refusals.Reads, &r.refusals.ReadsRefused, math.Ceil
	default:
		return false
	}
	n := float64(*seen)
	*seen++
	if round((n+1)*r.stress.Refuse) == round(n*r.stress.Refuse) {
		return false
	}
	*refused++
	return true
}

// Refusals are the relay's counts so far.
func (r *StoreRelay) Refusals() Refusals {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.refusals
}

// RefusalsFile is the file in the evidence directory where a stressed
// run's refusals land, each scenario's and their total, whether or not
// the run passes.
const RefusalsFile = "store-refusals.json"

// scenarioRefusals are one scenario's refusals.
type scenarioRefusals struct {
	Scenario string `json:"scenario"`
	Refusals
}

// writeRefusals writes RefusalsFile to out: the total of each scenario's
// refusals in scenarios, and the scenarios sorted by name.
func writeRefusals(out string, scenarios []scenarioRefusals) (Refusals, error) {
	var total Refusals
	for _, s := range scenarios {
		total = total.add(s.Refusals)
	}
	sorted := slices.SortedFunc(slices.Values(scenarios), func(a, b scenarioRefusals) int {
		return strings.Compare(a.Scenario, b.Scenario)
	})
	data, err := json.MarshalIndent(struct {
		Total     Refusals           `json:"total"`
		Scenarios []scenarioRefusals `json:"scenarios"`
	}{total, sorted}, "", "  ")
	if err == nil {
		err = os.MkdirAll(out, 0o750)
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(out, RefusalsFile), data, 0o600)
	}
	if err != nil {
		return total, fmt.Errorf("store refusals: %w", err)
	}
	return total, nil
}

func (r *StoreRelay) record(method, path string, status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, Call{Method: method, Path: path, Status: status})
}

// Calls are the calls the relay answered so far, in the order answered.
func (r *StoreRelay) Calls() []Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Call(nil), r.calls...)
}

// Close stops the relay.
func (r *StoreRelay) Close(ctx context.Context) error {
	if err := r.srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("store relay: %w", err)
	}
	return nil
}

// capRuns caps each run container the daemon creates from now on at cpus
// CPUs, until Close: Docker Desktop's one virtual machine cannot slow one
// container's clock or CPU on its own, so the cap is the harness's. A run
// container is any container labelled with docker.LabelRun, whatever its
// image. Each failure to cap one other than its being gone is kept for
// Close to return.
func (l *Launchers) capRuns(ctx context.Context, cpus float64) {
	ctx, l.stopCaps = context.WithCancel(context.WithoutCancel(ctx))
	events := l.cli.Events(ctx, client.EventsListOptions{
		Filters: client.Filters{}.
			Add("type", "container").Add("event", "create").Add("label", docker.LabelRun),
	})
	l.capped.Add(1)
	go func() {
		defer l.capped.Done()
		for {
			select {
			case m := <-events.Messages:
				_, err := l.cli.ContainerUpdate(ctx, m.Actor.ID, client.ContainerUpdateOptions{
					Resources: &container.Resources{NanoCPUs: int64(cpus * 1e9)},
				})
				if err != nil && !cerrdefs.IsNotFound(err) && !cerrdefs.IsConflict(err) &&
					ctx.Err() == nil {
					l.capErrs = errors.Join(l.capErrs, fmt.Errorf("cap %s: %w", m.Actor.ID, err))
				}
			case err := <-events.Err:
				if err != nil && ctx.Err() == nil {
					l.capErrs = errors.Join(l.capErrs, fmt.Errorf("docker events: %w", err))
				}
				return
			}
		}
	}()
}
