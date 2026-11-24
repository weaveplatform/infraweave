// Package assembly constructs the cloud domain, the AWS integrations that
// stacks attach, after state. API is its partial of the API's strict
// server, aliased CloudAPI; Stream is its migration stream, which
// internal/app registers after state's; Held are its columns for the
// orphan count; SpaceGuard and StackGuard are its say in space and stack
// deletes and moves; SpaceOf and AttachmentSpaceOf find the space the
// access domain decides an integration's and an attachment's operations
// on; Purge deletes a deleted account's integrations and attachments
// PurgeBatch rows at a time, which internal/app runs for each
// accessapi.Deleted; StackAwsAttachments is the
// stacksapi.StackAwsAttachments internal/app binds into stacks, whose view
// lists each stack's attachments, and StackCredentials the
// stacksapi.StackCredentials it binds there too, whose view lists the
// computed credential variables; Credentials is the cloudapi.Credentials
// it binds into runs, whose handovers hand each phase its credentials.
// Settings are cloud.aws, where the server reaches
// AWS STS with the credentials of the SDK's default chain.
//
// # Input and output
//
// None.
//
// # Dependencies
//
// It imports the domain's aws, core, http and pg packages, the access,
// cloud, features and stacks api packages, internal/kernel, the AWS SDK's STS
// client and platform/sql's integrity and migrate packages. Only
// internal/app and cmd/ import it, as the constructors rule enforces.
package assembly
