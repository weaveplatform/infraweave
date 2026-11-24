// Package http is the cloud domain's partial of the API's strict server:
// the AWS integration operations of the Cloud Integrations tag, the
// attachable integrations of a space and of a stack, the installation's
// AWS account, and a stack's legacy AWS role. Server answers each
// integration with its attachments, stored and computed, and maps the
// service's refusals to the problem register: a hidden or missing entity
// is not-found, a second read or write attachment invalid-state, a server
// without AWS credentials installation-unsupported and an STS outage
// upstream-error. A list's cursor is the last name of the page before.
//
// # Input and output
//
// None beyond the requests the generated server decodes and the
// responses it encodes.
//
// # Dependencies
//
// It imports the domain's core package, the access and stacks api
// packages, internal/kernel, platform/gen/api and platform/httpkit's
// problem and strict packages.
package http
