package middleware

import (
	"context"
	"net/http"
	"strconv"
)

type responseHeadersKey struct{}

// WithResponseHeaders binds the current writer's headers inside any recording
// middleware, so typed handlers' notices are captured by idempotency replay.
func WithResponseHeaders(ctx context.Context, headers http.Header) context.Context {
	return context.WithValue(ctx, responseHeadersKey{}, headers)
}

// Deprecate marks a known deprecated resource using an RFC 9745 date and a
// link to its scope/migration documentation. It declares no shutdown deadline.
func Deprecate(ctx context.Context, since int64, documentation string) {
	if headers, ok := ctx.Value(responseHeadersKey{}).(http.Header); ok {
		headers.Set("Deprecation", "@"+strconv.FormatInt(since, 10))
		headers.Add("Link", "<"+documentation+">; rel=\"deprecation\"")
	}
}
