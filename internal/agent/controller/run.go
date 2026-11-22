package controller

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// The leadership bounds every replica of one watch scope shares.
const (
	LeaseDuration = 15 * time.Second
	RenewDeadline = 10 * time.Second
	RetryPeriod   = 2 * time.Second
)

// Defaults of the reconciliation loop.
const (
	// DefaultResync is how often the leader rereads every watched pool.
	DefaultResync = 5 * time.Second
	// DefaultHeartbeat is how often a registered slot registers again,
	// which keeps its worker seen well within the server's default
	// worker.offline_after of three minutes.
	DefaultHeartbeat = time.Minute
	// DefaultBatch bounds the Worker resources one pass creates per pool.
	DefaultBatch = 16
)

// requestTimeout bounds each call to the installation.
const requestTimeout = 30 * time.Second

// Deps are what a Controller runs on. Identity names this replica in the
// Lease; Resync, Heartbeat and Batch take their defaults when zero, and
// HTTP, when nil, is a client trusting the system roots and ca_file's.
type Deps struct {
	Settings  Settings
	Clients   Clients
	HTTP      *http.Client
	Log       *slog.Logger
	Now       func() time.Time
	Version   string
	OS, Arch  string
	Identity  string
	Resync    time.Duration
	Heartbeat time.Duration
	Batch     int
}

// Controller reconciles the WorkerPool and Worker resources of its watch
// namespaces while it leads their Lease.
type Controller struct {
	settings  Settings
	kube      Clients
	remotes   *remotes
	log       *slog.Logger
	now       func() time.Time
	version   string
	os, arch  string
	identity  string
	resync    time.Duration
	heartbeat time.Duration
	batch     int

	mu      sync.Mutex
	seen    map[types.UID]time.Time
	synced  atomic.Bool
	leading atomic.Bool
}

// New is a Controller over d.
func New(d Deps) (*Controller, error) {
	doer := d.HTTP
	if doer == nil {
		var err error
		if doer, err = httpClient(d.Settings.CAFile); err != nil {
			return nil, err
		}
	}
	c := &Controller{
		settings: d.Settings, kube: d.Clients, log: d.Log, now: d.Now,
		version: d.Version, os: d.OS, arch: d.Arch, identity: d.Identity,
		resync: d.Resync, heartbeat: d.Heartbeat, batch: d.Batch, seen: map[types.UID]time.Time{},
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.log == nil {
		c.log = slog.New(slog.DiscardHandler)
	}
	if c.resync <= 0 {
		c.resync = DefaultResync
	}
	if c.heartbeat <= 0 {
		c.heartbeat = DefaultHeartbeat
	}
	if c.batch <= 0 {
		c.batch = DefaultBatch
	}
	c.remotes = &remotes{doer: doer, now: c.now, all: map[string]*remote{}}
	return c, nil
}

// httpClient trusts the system roots and, when caFile is set, its PEM
// certificates; there is no switch that skips verification.
func httpClient(caFile string) (*http.Client, error) {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("worker controller: the default HTTP transport is replaced") //nolint:err113 // a process misconfiguration
	}
	transport = transport.Clone()
	if caFile != "" {
		pem, err := os.ReadFile(caFile) //nolint:gosec // an operator-configured trust bundle
		if err != nil {
			return nil, fmt.Errorf("%w: read ca_file: %w", ErrSettings, err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%w: ca_file holds no PEM certificate", ErrSettings)
		}
		transport.TLSClientConfig.RootCAs = roots
	}
	return &http.Client{Transport: transport, Timeout: requestTimeout}, nil
}

// Synced reports whether a pass has read the watched resources, for the
// readiness probe; a standby replica that never leads stays ready.
func (c *Controller) Synced(context.Context) error {
	if !c.synced.Load() && c.leading.Load() {
		return errors.New("the first reconciliation has not finished") //nolint:err113 // a probe's reason
	}
	return nil
}

// Leading reports whether this replica holds the Lease.
func (c *Controller) Leading() bool { return c.leading.Load() }

// Run takes part in leader election until ctx ends, reconciling while it
// leads. A replica that loses the Lease stops reconciling at once and
// stands for election again; the Lease, resource versions and the
// server's hosting binding fence any overlap of two leaders.
func (c *Controller) Run(ctx context.Context) error {
	lock := &resourcelock.LeaseLock{
		LeaseMeta:  metaFor(c.settings.LeaderElection.Name, c.settings.LeaderElection.Namespace),
		Client:     c.kube.Coordination,
		LockConfig: resourcelock.ResourceLockConfig{Identity: c.identity},
	}
	for ctx.Err() == nil {
		elector, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
			Lock: lock, LeaseDuration: LeaseDuration, RenewDeadline: RenewDeadline, RetryPeriod: RetryPeriod,
			ReleaseOnCancel: true, Name: c.settings.LeaderElection.Name,
			Callbacks: leaderelection.LeaderCallbacks{
				OnStartedLeading: c.lead,
				OnStoppedLeading: func() { c.leading.Store(false) },
			},
		})
		if err != nil {
			return fmt.Errorf("worker controller: leader election: %w", err)
		}
		elector.Run(ctx)
	}
	return nil
}

// lead reconciles every watched pool each resync until ctx ends.
func (c *Controller) lead(ctx context.Context) {
	c.leading.Store(true)
	c.log.InfoContext(ctx, "leading worker controller", "identity", c.identity)
	ticker := time.NewTicker(c.resync)
	defer ticker.Stop()
	for {
		c.Pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Pass reconciles every WorkerPool of the watch namespaces once, logging
// each failure, which its resource's status also records.
func (c *Controller) Pass(ctx context.Context) {
	listed := true
	for _, namespace := range c.settings.WatchNamespaces {
		pools, err := c.pools(ctx, namespace)
		if err != nil {
			listed = false
			c.log.WarnContext(ctx, "worker pools unreadable", "namespace", namespace, "error", err.Error())
			continue
		}
		for _, wp := range pools {
			if err := c.reconcile(ctx, wp); err != nil && ctx.Err() == nil {
				c.log.WarnContext(ctx, "worker pool reconciliation", "namespace", wp.Namespace, "name", wp.Name, "error", err.Error())
			}
		}
	}
	if listed {
		c.synced.Store(true)
	}
}

func metaFor(name, namespace string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Namespace: namespace}
}
