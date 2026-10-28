// Package problem writes RFC 9457 problem details, the body of every error
// response the deploy API sends (the Problem schema in
// api/src/deploy/components-common.yaml), and adapts handler errors to them.
package problem

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// ContentType is the media type of every problem body.
const ContentType = "application/problem+json"

// TypePrefix starts every problem type URI; the slugs are listed in
// docs/research/decisions/0003-api-conventions.md.
const TypePrefix = "urn:infraweave:problem:"

// Problem is one problem detail. Field names match the Problem schema.
type Problem struct {
	Type     string `json:"type,omitempty"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
}

// Write sends p with its status. The status line and the body's status are
// the same number, as RFC 9457 requires.
func Write(w http.ResponseWriter, p Problem) {
	w.Header().Set("Content-Type", ContentType)
	w.WriteHeader(p.Status)
	// The header is already sent; a failed write means the client has gone.
	_ = json.NewEncoder(w).Encode(p)
}

// New is a problem for status with the standard reason phrase as its title.
func New(status int, detail string) Problem {
	return Problem{Title: http.StatusText(status), Status: status, Detail: detail}
}

// NotImplementedError is returned by the generated UnimplementedStrict for
// every operation a family hasn't implemented yet.
type NotImplementedError struct {
	// OperationID is the operation's operationId in the deploy API.
	OperationID string
	// Route is the method and path template, such as "GET /stacks".
	Route string
}

func (e *NotImplementedError) Error() string {
	return fmt.Sprintf("%s (%s) is not implemented yet", e.OperationID, e.Route)
}

// RequestError answers a request the generated handler couldn't decode
// (a malformed body or parameter) with 400.
func RequestError(w http.ResponseWriter, _ *http.Request, err error) {
	Write(w, New(http.StatusBadRequest, err.Error()))
}

// ResponseError answers an error a strict handler returned: 501 naming the
// operation for a stub, and 500 otherwise. The 500 detail is generic so
// internal errors never reach the client.
func ResponseError(w http.ResponseWriter, _ *http.Request, err error) {
	if nie, ok := errors.AsType[*NotImplementedError](err); ok {
		Write(w, New(http.StatusNotImplemented, nie.Error()))
		return
	}
	Write(w, New(http.StatusInternalServerError, "The server failed to handle the request."))
}
