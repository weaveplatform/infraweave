package config

import "github.com/weaveplatform/infraweave/internal/platform/rego"

// ErrPolicy reports an invalid policy execution or inherited event budget.
var ErrPolicy = rego.ErrSettings

// PolicySettings holds the policy runtime's strict YAML settings and the
// push-event deadline shared by intake and durable policy jobs.
//
// ref: decisions 0009 and 0012
type PolicySettings = rego.Settings

// PolicyTimeouts bounds policy evaluation, combined work and VCS event intake.
type PolicyTimeouts = rego.Timeouts

func defaultPolicy() PolicySettings { return rego.Defaults() }
