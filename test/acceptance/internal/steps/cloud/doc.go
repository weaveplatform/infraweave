// Package cloud holds the acceptance steps of features/cloud: AWS
// integrations, the attachments a stack lists, the AWS STS their trust
// checks and runs' handovers assume roles through, which a fake serves,
// and the credentials it issues runs.
//
// # Input and output
//
// It serves the fake AWS STS on a local listener, sets the scenario's
// servers' cloud.aws settings and AWS environment, reads the answers the
// scenario's requests recorded and the servers' logs, and searches the
// scenario's bucket for the credentials the fake issued.
//
// # Dependencies
//
// It builds on harness.World, test/deps' object store and
// internal/cloud/awsfake.
package cloud
