// Package entrafake is Microsoft Entra ID and the Microsoft Graph
// application routes for tests. Fake holds the tenants' registered clients,
// each with a client secret or a federated assertion it accepts, and the
// applications created through Graph. It answers a tenant's OpenID
// discovery document and its token endpoint's client credentials grant as
// Entra does, so azidentity's own credentials are what call it, and
// POST, GET and DELETE of /v1.0/applications as Graph does, and the
// listing a displayName eq filter selects, for a bearer token it issued.
// It records every call with the client, the grant and the answer's
// status. FailNext makes the next Graph requests answer 500, or 503 or 429
// with Retry-After, or create the application and then answer 500, as a
// Graph that fails after its write does, and DelayReplication makes each application
// created afterwards answer 404 to its first reads and deletes, as Graph's
// replication delay does, so a client's retries are tested without the
// real service.
//
// # Input and output
//
// It serves GET /{tenant}/v2.0/.well-known/openid-configuration, POST
// /{tenant}/oauth2/v2.0/token with a form body, and POST and GET
// /v1.0/applications, GET and DELETE /v1.0/applications/{id} with a JSON
// body; the GET of /v1.0/applications takes $filter=displayName eq '<name>'
// alone, and answers 400 Request_UnsupportedQuery to any other filter. An unknown client or a wrong secret or assertion answers 401
// invalid_client; a Graph request without one of its tokens answers 401
// InvalidAuthenticationToken; an application without a display name or
// with another sign-in audience than Graph's answers 400 Request_BadRequest;
// an unknown application answers 404 Request_ResourceNotFound. Start
// serves it over TLS on a loopback port, since azidentity reaches an
// authority over https alone, and answers the certificate to trust.
//
// # Dependencies
//
// It imports chi and google/uuid. The azure client's tests and the
// acceptance suite construct it.
package entrafake
