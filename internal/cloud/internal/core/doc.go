// Package core is the cloud domain's model and rules for AWS and Azure
// integrations. An AWS integration in a space names a role in a customer's
// AWS account, the region whose STS endpoint assumes it, the session
// duration (900 to 3600 seconds when the server assumes the role, up to
// 43200 when a private worker does) and, for a worker's integration
// alone, a custom external ID. Service creates, changes and deletes
// integrations, and attaches them to the stacks within reach for read,
// write or both: a stack in a space reaches the integrations of that
// space and of each ancestor it climbs to while every space on the way
// inherits, and keeps at most one read and one write attachment. An
// integration with auto-attachment enabled and labelled
// autoattach:<label>, autoattach_read:<label> or autoattach_write:<label>
// serves every stack in reach that carries the label, on the sides the
// label names; such an attachment is computed when it is read, never
// stored, and a stored attachment outranks it. Attached computes the
// attachments of a whole set of stacks from one read of their stored
// attachments and one of the integrations that may attach to them, and
// AwsAttachments answers the stack view those of each stack whose
// integration lives in a space its caller may read, with
// AttachmentExternalID the external ID each shows. Attaching an integration
// the server assumes first assumes its role through Roles with the
// external ID of each side, and a role that refuses answers TrustDetail.
// ExternalID, LegacyExternalID and SessionName are the fixed formats role
// assumption passes, and PolicyStatement the trust policy statement a
// customer writes. ForPhase answers a run's phase the credentials of the
// attachment that serves its side: it assumes an integration the server
// assumes once, with PhaseAssumption's external ID, session name,
// duration, region and session tags, and answers the four computed AWS
// variables, each a secret and kept nowhere; it answers a worker's
// integration as the assumption the worker makes; and a refusal or failure
// of STS is a *kernel.Invalid naming the integration and STS's code.
// CredentialVariables names those variables for each stack an attachment
// serves. A stack's legacy AWS role is a legacy integration bound
// to that stack. SpaceGuard and StackGuard refuse a space delete or move,
// and a stack move, that would leave a stored attachment out of reach,
// and delete a deleted stack's attachments and legacy integration. Every
// write records an AWS_INTEGRATION audit entry in its transaction.
//
// An Azure integration in a space names a customer's Entra tenant, fixed
// at creation, and the multi-tenant Entra application CreateAzure creates
// for it through Applications, named by DisplayName, whose admin consent
// AdminConsentURL asks for. Graph is called outside the transaction that
// stores the integration, and an application whose row is not written is
// deleted again; DeleteAzure deletes the application before the row, so a
// Graph failure keeps the integration. Without Applications the
// installation has no cloud.azure, and a create or delete answers
// ErrAzureUnsupported. SetConsent records an admin's statement that
// consent was granted, or no longer is, with its actor and time, never
// asking Entra. Azure attachments follow the AWS rules, with one read and
// one write per stack and an optional subscription each; auto-attachment
// needs a default subscription, which automatic attachments use. The
// guards and PurgeAzure cover Azure integrations too, and every Azure
// write records an AZURE_INTEGRATION audit entry.
//
// # Input and output
//
// None: the stores, the spaces, the accounts, the stacks, the authorizer,
// the server's AWS identity and the Graph client are interfaces its
// callers pass.
//
// # Dependencies
//
// It imports the access, cloud and stacks api packages, internal/kernel
// and platform/tx. The cloud domain's assembly constructs it over the pg
// stores, the STS client and the Graph client.
package core
