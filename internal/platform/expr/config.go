package expr

import (
	"errors"
	"fmt"
	"time"
)

// Config holds the limits every evaluation runs under, keyed below
// expressions.
//
// ref: decision 0024 "Evaluation and limits"
type Config struct {
	// CostLimit is the most cel-go cost units one evaluation may spend.
	CostLimit uint64 `mapstructure:"cost_limit"`
	// Timeout interrupts an evaluation that runs longer.
	Timeout time.Duration `mapstructure:"timeout"`
	// MaxSource is the longest source, in bytes, that compiles.
	MaxSource int `mapstructure:"max_source"`
	// MaxResult is the largest result, in bytes of its text, that an
	// evaluation may return.
	MaxResult int `mapstructure:"max_result"`
}

// Defaults are the limits a process starts with.
//
// ref: decision 0024 "Evaluation and limits"
func Defaults() Config {
	return Config{
		CostLimit: 1_000_000,
		Timeout:   time.Second,
		MaxSource: 16 << 10,
		MaxResult: 1 << 20,
	}
}

// ErrConfig reports a limit the engine could not run with.
var ErrConfig = errors.New("invalid expressions configuration")

// Validate rejects a limit that is not positive. Errors name the key.
func (c Config) Validate() error {
	var errs []error
	if c.CostLimit == 0 {
		errs = append(errs, fmt.Errorf("%w: expressions.cost_limit must be positive", ErrConfig))
	}
	if c.Timeout <= 0 {
		errs = append(errs, fmt.Errorf(
			"%w: expressions.timeout must be positive, got %s", ErrConfig, c.Timeout))
	}
	if c.MaxSource <= 0 {
		errs = append(errs, fmt.Errorf(
			"%w: expressions.max_source must be positive, got %d", ErrConfig, c.MaxSource))
	}
	if c.MaxResult <= 0 {
		errs = append(errs, fmt.Errorf(
			"%w: expressions.max_result must be positive, got %d", ErrConfig, c.MaxResult))
	}
	return errors.Join(errs...)
}
