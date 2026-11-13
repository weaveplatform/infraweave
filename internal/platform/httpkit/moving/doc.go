// Package moving keeps a transfer's connection for as long as it moves.
// Start begins a verified upload's or download's first step; each read
// of Reader's body or write of Writer's response is another, moving the
// connection's deadlines to the stall past it, so an object of any size
// moves for as long as it keeps moving and a transfer that stalls loses
// its connection. Stall is the step the API listener's transfers share:
// the fs driver's signed URLs and the state backend.
//
// ref: decision 0003 § "API surfaces"
//
// # Input and output
//
// It sets the deadlines of a response's connection through
// http.ResponseController and passes every byte through unchanged.
// Nothing is logged.
//
// # Dependencies
//
// It imports the standard library alone. The fs objectstore driver and
// the state domain's backend call it.
package moving
