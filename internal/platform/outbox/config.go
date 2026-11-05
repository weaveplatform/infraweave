package outbox

import (
	"errors"
	"fmt"
	"time"
)

// Config holds the outbox settings, keyed below events.
type Config struct {
	// Retention keeps an event after its fan-out, the window a stream
	// client may resume within (decision 0015).
	Retention time.Duration `mapstructure:"retention"`
}

// Defaults are decision 0015's settings.
func Defaults() Config { return Config{Retention: 72 * time.Hour} }

// ErrConfig reports a setting the outbox could not run with.
var ErrConfig = errors.New("invalid events configuration")

// Validate rejects a retention that is not positive. Errors name the key.
func (c Config) Validate() error {
	if c.Retention <= 0 {
		return fmt.Errorf("%w: events.retention must be positive, got %s", ErrConfig, c.Retention)
	}
	return nil
}
