// Package observe records VCS-owned observations using bounded metric labels.
// Business decisions remain in the VCS core; adapters only export their counts.
//
// # Input and output
//
// CheckActionRefused takes a meter and returns a callback for committed button
// refusals, or the instrument-construction error. The callback counts only the
// fixed VCS reason supplied by the core; target and sender data are not labels.
//
// # Dependencies
//
// It uses the OpenTelemetry metric API and attributes. VCS assembly supplies
// the meter; the core invokes the callback after its transaction commits.
// No exporter or global meter provider is installed here.
package observe
