// Package cloud holds the acceptance steps of features/cloud: AWS and
// Azure integrations, the attachments a stack lists, the AWS STS their
// trust checks and runs' handovers assume roles through, and the Entra ID
// and Microsoft Graph that create and delete Azure integrations'
// applications, which fakes serve, and the credentials STS issues runs.
//
// # Input and output
//
// It serves the fake AWS STS on a local listener and the fake Entra ID and
// Graph over TLS on another, writing its certificate to the scenario's
// directory; sets the scenario's servers' cloud.aws and cloud.azure
// settings and their AWS and AZURE_* environment; reads the answers the
// scenario's requests recorded, the fakes' calls and applications and the
// servers' logs; and searches the scenario's bucket for the credentials
// the fake STS issued.
//
// # Dependencies
//
// It builds on harness.World, test/deps' object store,
// internal/cloud/awsfake and internal/cloud/entrafake.
package cloud
