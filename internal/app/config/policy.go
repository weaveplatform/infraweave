package config

import (
	"errors"
	"fmt"
	"time"
)

// ErrPolicy reports an invalid event processing deadline.
var ErrPolicy = errors.New("invalid policy configuration")

// PolicySettings holds the event processing deadline shared by intake and later
// policy jobs. A delivery's children inherit its received-time deadline.
//
// ref: decision 0012
type PolicySettings struct {
	Timeouts PolicyTimeouts `mapstructure:"timeouts"`
}

// PolicyTimeouts bounds push event processing, independently of queue attempts.
type PolicyTimeouts struct {
	PushEvent time.Duration `mapstructure:"push_event"`
}

func defaultPolicy() PolicySettings {
	return PolicySettings{Timeouts: PolicyTimeouts{PushEvent: 10 * time.Minute}}
}

// Validate refuses an event deadline that is already exhausted at intake.
func (p PolicySettings) Validate() error {
	if p.Timeouts.PushEvent <= 0 {
		return fmt.Errorf("%w: policy.timeouts.push_event must be positive", ErrPolicy)
	}
	return nil
}
