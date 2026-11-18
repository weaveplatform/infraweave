// Package metrics records committed sink delivery outcomes through
// OpenTelemetry. It installs no exporter or global meter provider.
//
// # Input and output
//
// New takes a meter and creates infraweave.events.deliveries. Add records the
// commit_checks sink with delivered or skipped outcomes, after the enclosing
// transaction commits.
//
// # Dependencies
//
// It implements the events port Metrics with the OpenTelemetry metric API and
// bounded attributes. Assembly supplies the process’s meter; the delivery
// core owns when outcomes commit.
package metrics
