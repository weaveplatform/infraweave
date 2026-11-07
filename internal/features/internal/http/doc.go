// Package http is the features domain's HTTP adapter: Middleware, the chi
// middleware internal/app runs after authentication, which resolves each
// request's Gate (its operation's capability and the gated values its body
// may set) and asks an Allower for each feature the request needs,
// answering 403 feature-disabled with the feature, or feature-not-in-tier
// with the feature and the tiers that hold it.
//
// # Input and output
//
// It reads a request's body when its operation has gated options, and
// restores it for the handler.
//
// # Dependencies
//
// It imports featuresapi, the domain's features package for Refusal and
// platform/httpkit/problem. The features domain's assembly constructs it.
package http
