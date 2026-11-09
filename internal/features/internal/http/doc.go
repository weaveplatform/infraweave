// Package http is the features domain's HTTP adapter: Middleware, the chi
// middleware internal/app runs after authentication, which resolves each
// request's Gate (its operation's capability and the gated values its body
// may set) and asks an Allower for each feature the request needs,
// answering 403 feature-disabled with the feature, or feature-not-in-tier
// with the feature and the tiers that hold it. OperatorServer is the
// domain's partial of the operator API's strict server: the tiers, the
// billing switch and the default tier, and an organization's tier,
// entitlement and grants, finding the organization by its slug through the
// Organizations it is given; it answers 422 validation-failed for a tier or
// grant the catalogue refuses or a tier that does not exist, 409 for a
// name in use, for Enterprise and for a tier in use, and 404 for what does
// not exist. Server is the domain's partial of the API's strict server:
// tier, tierFeatures and seats answer from the caller's organization, with
// the seats in use counted through the SeatCounter it is given;
// billedExternally answers true, billingSubscription and onTrialUntil
// null, the add-on and plan lists and evaluateFeatureFlags nothing, and
// the five subscription mutations 403 installation-unsupported.
//
// # Input and output
//
// It reads a request's body when its operation has gated options, and
// restores it for the handler.
//
// # Dependencies
//
// It imports the catalogue (internal/features), featuresapi, the domain's
// core package for Refusal and the service, internal/kernel,
// platform/gen/api, platform/gen/operator, platform/httpkit/page and
// platform/httpkit/problem. The features domain's assembly constructs it.
package http
