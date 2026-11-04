// Package page is decision 0003's opaque cursors for family handlers.
//
// Encode turns a kernel.Cursor, a keyset position, into base64url-encoded
// versioned JSON that names the list it came from; Decode checks the version
// and the list and reads the position back. A list reads WHERE (sort_key, id)
// > (cursor) ORDER BY sort_key, id LIMIT kernel.PageLimit + 1, and encodes the
// last row it returns as the next cursor when the extra row shows there is
// another page.
//
// # Input and output
//
// None beyond its arguments: it encodes and decodes strings in memory. A
// cursor that does not decode is returned as a *problem.Error of type
// invalid-cursor, for the handler to return as it is.
//
// # Dependencies
//
// It imports internal/kernel for the cursor and page values and
// platform/httpkit/problem for the error. Each domain's HTTP adapter imports
// it for its list operations.
package page
