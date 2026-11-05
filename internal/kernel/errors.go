package kernel

import "errors"

// ErrUnavailable marks a failure of a dependency infraweave cannot reach
// now, such as PostgreSQL, rather than a failure of the request. The HTTP
// layer answers it with 503 service-unavailable and Retry-After, so the
// client tries again; any other unexpected failure is a 500, which it does
// not.
var ErrUnavailable = errors.New("unavailable")
