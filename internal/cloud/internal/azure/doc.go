// Package azure is the server's Microsoft Graph client for Azure
// integrations: Client creates a dedicated multi-tenant Entra application
// for an integration, which asks Graph's delegated User.Read so a
// customer's admin has a permission to consent to, and deletes it again.
// It authenticates as the operator's registered application, cloud.azure
// in Settings, through azidentity's default credential chain. Graph's
// throttling, its server errors and the 404s of an object it has not yet
// replicated are retried with backoff, honouring Retry-After, within the
// call's deadline; what is left is a *GraphError. A create that fails in
// a way Graph may have answered after writing the application looks the
// application up by its display name before it posts again, adopting
// one, failing on more than one and deleting what it finds when it fails,
// so a retry leaves no application behind. Applications is the
// client as the service's core.Applications, its failures as
// *core.GraphFailure.
//
// ref: report 12 § "3.1 Behaviour"
// ref: decision 0021 "Vendor-bound operations"
//
// # Input and output
//
// Each call is HTTPS requests to Graph, or Settings.GraphEndpoint, with a
// token from Entra's authority, or Settings.AuthorityHost, which the
// default chain obtains from the AZURE_* environment, a workload
// identity's federated token file or a managed identity. Settings.CAFile
// adds roots both are trusted with. Errors name the operation, Graph's
// status and error code, never a credential, and nothing is logged.
//
// # Dependencies
//
// It imports azcore, azidentity and the domain's core package. The cloud
// domain constructs it; its tests call the entrafake Entra and Graph.
package azure
