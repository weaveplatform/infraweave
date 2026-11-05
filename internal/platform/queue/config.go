package queue

import (
	"errors"
	"fmt"
	"time"
)

// Config holds the queue settings, keyed below queue.
type Config struct {
	// Lease is how long a claim holds a job before another replica may
	// take it over; a running handler renews it every third of it.
	Lease time.Duration `mapstructure:"lease"`
	// PollInterval is how often an idle queue claims when no notification
	// wakes it, and how often lapsed leases are recovered.
	PollInterval time.Duration `mapstructure:"poll_interval"`
	// SchedulerTick is how often the ticker claims due schedules.
	SchedulerTick time.Duration `mapstructure:"scheduler_tick"`
	// ShutdownGrace is how long running handlers may take to finish on
	// shutdown before they are cancelled and their jobs handed back.
	ShutdownGrace time.Duration `mapstructure:"shutdown_grace"`
	// CompletedRetention keeps succeeded and cancelled jobs.
	CompletedRetention time.Duration `mapstructure:"completed_retention"`
	// DeadRetention keeps dead jobs for the operator.
	DeadRetention time.Duration `mapstructure:"dead_retention"`
	// Queues sets each logical queue's workers per replica.
	Queues PerQueue `mapstructure:"queues"`
}

// PerQueue holds one setting block per logical queue. Its fields are named,
// not a map, so each worker count has its own environment variable, such
// as INFRAWEAVE_QUEUE_QUEUES_VCS_INBOX_WORKERS.
type PerQueue struct {
	Default     QueueConfig `mapstructure:"default"`
	Events      QueueConfig `mapstructure:"events"`
	VCSInbox    QueueConfig `mapstructure:"vcs-inbox"`
	Deliveries  QueueConfig `mapstructure:"deliveries"`
	Scheduler   QueueConfig `mapstructure:"scheduler"`
	Flows       QueueConfig `mapstructure:"flows"`
	Maintenance QueueConfig `mapstructure:"maintenance"`
}

// QueueConfig holds one logical queue's settings.
type QueueConfig struct {
	// Workers is how many of the queue's jobs one replica runs at once; 0
	// runs none there.
	Workers int `mapstructure:"workers"`
}

// Defaults are decision 0011's settings. The lease matches Spacelift's
// 300-second visibility timeout.
func Defaults() Config {
	return Config{
		Lease:              5 * time.Minute,
		PollInterval:       time.Second,
		SchedulerTick:      5 * time.Second,
		ShutdownGrace:      30 * time.Second,
		CompletedRetention: time.Hour,
		DeadRetention:      30 * 24 * time.Hour,
		Queues: PerQueue{
			Default:     QueueConfig{Workers: 8},
			Events:      QueueConfig{Workers: 8},
			VCSInbox:    QueueConfig{Workers: 4},
			Deliveries:  QueueConfig{Workers: 8},
			Scheduler:   QueueConfig{Workers: 2},
			Flows:       QueueConfig{Workers: 8},
			Maintenance: QueueConfig{Workers: 1},
		},
	}
}

// Workers is queue n's worker count.
func (c Config) Workers(n Name) int {
	return c.Queues.byName()[n].Workers
}

func (q PerQueue) byName() map[Name]QueueConfig {
	return map[Name]QueueConfig{
		Default: q.Default, Events: q.Events, VCSInbox: q.VCSInbox, Deliveries: q.Deliveries,
		Scheduler: q.Scheduler, Flows: q.Flows, Maintenance: q.Maintenance,
	}
}

// ErrConfig marks an invalid queue setting.
var ErrConfig = errors.New("invalid queue configuration")

// Validate rejects durations a queue could not run with and negative worker
// counts. Errors name the key.
func (c Config) Validate() error {
	var errs []error
	for _, d := range []struct {
		key string
		v   time.Duration
	}{
		{"lease", c.Lease},
		{"poll_interval", c.PollInterval},
		{"scheduler_tick", c.SchedulerTick},
		{"completed_retention", c.CompletedRetention},
		{"dead_retention", c.DeadRetention},
	} {
		if d.v <= 0 {
			errs = append(
				errs,
				fmt.Errorf("%w: queue.%s must be positive, got %s", ErrConfig, d.key, d.v),
			)
		}
	}
	if c.ShutdownGrace < 0 {
		errs = append(errs, fmt.Errorf("%w: queue.shutdown_grace must not be negative, got %s",
			ErrConfig, c.ShutdownGrace))
	}
	byName := c.Queues.byName()
	for _, n := range Queues {
		if w := byName[n].Workers; w < 0 {
			errs = append(
				errs,
				fmt.Errorf("%w: queue.queues.%s.workers must not be negative, got %d",
					ErrConfig, n, w),
			)
		}
	}
	return errors.Join(errs...)
}
