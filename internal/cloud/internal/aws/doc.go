// Package aws is the server's STS client for AWS integrations: Client
// answers the account of the server's own identity with
// sts:GetCallerIdentity, kept after its first success, and assumes a
// customer's role with sts:AssumeRole, passing the external ID, session
// name, duration and session tags the caller chose. STS's AccessDenied,
// a trust policy or its external ID condition refusing the server, is
// *Refused; with no credentials for the server's identity every call is
// ErrNoCredentials and STS is not called.
//
// ref: decision 0006 § "AWS external ID and session name"
//
// # Input and output
//
// Each call is one HTTPS request to STS, or to Settings.STSEndpoint when it
// is set, with credentials from the AWS SDK's default chain, which reads
// the AWS_* environment, the shared config and credentials files, web
// identity, container and EC2 instance credentials. A role's call goes to
// the regional endpoint of the input's region, the environment's, or
// us-east-1. Errors name the role and STS's error code, never a
// credential, and nothing is logged.
//
// # Dependencies
//
// It imports aws-sdk-go-v2's config and sts, and smithy-go for STS's error
// codes. The cloud domain constructs it; its tests call the awsfake STS.
package aws
