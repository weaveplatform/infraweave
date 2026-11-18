package config

import (
	"errors"
	"fmt"
	"time"

	eventsassembly "github.com/weaveplatform/infraweave/internal/events/assembly"
	"github.com/weaveplatform/infraweave/internal/platform/outbox"
)

// EventsSettings composes the platform outbox's events.retention with the
// events domain's delivery-record retention under the existing key.
type EventsSettings struct {
	Retention  time.Duration           `mapstructure:"retention"`
	Deliveries eventsassembly.Settings `mapstructure:"deliveries"`
}

func defaultEventsSettings() EventsSettings {
	return EventsSettings{Retention: outbox.Defaults().Retention, Deliveries: eventsassembly.Defaults()}
}

// Validate retains fan-out identity longer than an event can be replayed.
func (s EventsSettings) Validate() error {
	var bounds error
	if s.Deliveries.Retention <= s.Retention {
		bounds = fmt.Errorf("%w: events.deliveries.retention must exceed events.retention", eventsassembly.ErrConfig)
	}
	return errors.Join(outbox.Config{Retention: s.Retention}.Validate(), s.Deliveries.Validate(), bounds)
}
