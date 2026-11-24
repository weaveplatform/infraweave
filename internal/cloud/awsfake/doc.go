// Package awsfake is AWS STS for tests: Fake holds the callers it knows by
// access key ID and the roles they assume, each with the accounts and
// principals its trust policy names, an sts:ExternalId condition matched
// as StringLike matches, and a maximum session duration. It answers
// AssumeRole and GetCallerIdentity over the awsQuery protocol as STS does,
// so the SDK's own STS client is what calls it, and records every call
// with its parameters, the region of its signature and its answer.
// FailNext and FailRole make it answer with a server error, throttling, a
// body that is not XML or no answer at all, so the client's handling of
// each is tested without the real service.
//
// # Input and output
//
// It answers POST / with a form body, or GET / with the same parameters
// in the query. The caller is the access key ID in the SigV4
// Authorization header's credential scope; the signature is not checked,
// and an unknown or missing key answers 403 InvalidClientTokenId. A role
// that does not exist, does not trust the caller or whose external ID
// condition fails answers 403 AccessDenied; a session name or duration
// outside STS's constraints answers 400 ValidationError. Credentials it
// issues start ASIA and are callers in turn, as the assumed role.
//
// # Dependencies
//
// It imports chi. The aws client's tests and the acceptance suite
// construct it.
package awsfake
