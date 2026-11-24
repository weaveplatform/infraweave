// Package cloud holds the acceptance steps of features/cloud: AWS
// integrations, the attachments a stack lists, and the AWS STS their
// trust checks assume roles through, which a fake serves.
//
// # Input and output
//
// It serves the fake AWS STS on a local listener, sets the scenario's
// servers' cloud.aws settings and AWS environment, and reads the answers
// the scenario's requests recorded.
//
// # Dependencies
//
// It builds on harness.World and internal/cloud/awsfake.
package cloud
