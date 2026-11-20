package rego

import (
	"errors"
	"fmt"
	"time"
)

const (
	// CacheEntries bounds the process's prepared-query LRU.
	CacheEntries = 2048
	// Concurrency bounds simultaneous evaluations, including their compilation.
	Concurrency = 8
	// PrintBytes bounds captured print output per evaluation.
	PrintBytes = 64 << 10
	// MessageCount and MessageBytes bound retained messages per rule.
	MessageCount = 100
	MessageBytes = 4 << 10
)

// Settings is the operator's policy execution budget, shared with event intake.
type Settings struct {
	Timeouts           Timeouts `mapstructure:"timeouts"`
	MaxInputBytes      int64    `mapstructure:"max_input_bytes"`
	MaxBodyBytes       int64    `mapstructure:"max_body_bytes"`
	MaxSimulationBytes int64    `mapstructure:"max_simulation_bytes"`
}

// Timeouts bounds each policy type, an attached collection and a VCS event.
type Timeouts struct {
	Plan           time.Duration `mapstructure:"plan"`
	Push           time.Duration `mapstructure:"push"`
	Notification   time.Duration `mapstructure:"notification"`
	Login          time.Duration `mapstructure:"login"`
	Approval       time.Duration `mapstructure:"approval"`
	Trigger        time.Duration `mapstructure:"trigger"`
	Intent         time.Duration `mapstructure:"intent"`
	Task           time.Duration `mapstructure:"task"`
	Initialization time.Duration `mapstructure:"initialization"`
	Access         time.Duration `mapstructure:"access"`
	Combined       time.Duration `mapstructure:"combined"`
	PushEvent      time.Duration `mapstructure:"push_event"`
}

// ErrSettings reports an invalid configured policy budget.
var ErrSettings = errors.New("invalid policy configuration")

// Defaults returns the documented server and launcher execution budgets.
func Defaults() Settings {
	return Settings{Timeouts: Timeouts{
		Plan: 45 * time.Second, Push: 10 * time.Second, Notification: 10 * time.Second,
		Login: 5 * time.Second, Approval: 5 * time.Second, Trigger: 5 * time.Second,
		Intent: 5 * time.Second, Task: 5 * time.Second, Initialization: 5 * time.Second,
		Access: 5 * time.Second, Combined: 5 * time.Minute, PushEvent: 10 * time.Minute,
	}, MaxInputBytes: 64 << 20, MaxBodyBytes: 256 << 10, MaxSimulationBytes: 4 << 20}
}

// Validate refuses exhausted deadlines and nonpositive size budgets.
func (s Settings) Validate() error {
	for name, timeout := range map[string]time.Duration{
		"plan": s.Timeouts.Plan, "push": s.Timeouts.Push, "notification": s.Timeouts.Notification,
		"login": s.Timeouts.Login, "approval": s.Timeouts.Approval, "trigger": s.Timeouts.Trigger,
		"intent": s.Timeouts.Intent, "task": s.Timeouts.Task, "initialization": s.Timeouts.Initialization,
		"access": s.Timeouts.Access, "combined": s.Timeouts.Combined, "push_event": s.Timeouts.PushEvent,
	} {
		if timeout <= 0 {
			return fmt.Errorf("%w: policy.timeouts.%s must be positive", ErrSettings, name)
		}
	}
	for name, limit := range map[string]int64{"max_input_bytes": s.MaxInputBytes, "max_body_bytes": s.MaxBodyBytes, "max_simulation_bytes": s.MaxSimulationBytes} {
		if limit <= 0 {
			return fmt.Errorf("%w: policy.%s must be positive", ErrSettings, name)
		}
	}
	return nil
}

func (s Settings) timeout(kind Type) time.Duration {
	switch kind {
	case Plan:
		return s.Timeouts.Plan
	case Push:
		return s.Timeouts.Push
	case Notification:
		return s.Timeouts.Notification
	case Login:
		return s.Timeouts.Login
	case Approval:
		return s.Timeouts.Approval
	case Trigger:
		return s.Timeouts.Trigger
	case Intent:
		return s.Timeouts.Intent
	case Task:
		return s.Timeouts.Task
	case Initialization:
		return s.Timeouts.Initialization
	case Access:
		return s.Timeouts.Access
	}
	return 0
}
