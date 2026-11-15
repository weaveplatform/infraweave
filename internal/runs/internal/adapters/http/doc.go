// Package http is the runs domain's HTTP adapter: Server, the domain's
// partial of the API's strict server. It triggers runs through the
// trigger service, answering a locked stack 403 forbidden and a disabled
// stack or one with no commit to run 409 invalid-state; reads runs, a
// stack's lists of runs and a search of them through the read service,
// each as the API's Run with its history oldest first, its comments, and
// its guards as canRetry, canPromote and the sentences of their
// blockers; acts on runs through the control service, answering a
// *control.Refused 409 invalid-state with what the action needs and a
// *control.Blocked 403 forbidden with the blocking variant the caller
// lacks; reads a run's changes through the changes service; and reads,
// downloads and deletes a run's logs through the logs service, whose
// retention in days runLogRetentionDays answers. The domain's other
// operations answer 501, or answer from the stacks domain's partial
// where it serves them empty. Its tests hold internal/kernel's run
// states, run types, stack states and run priorities to the API's enums,
// and RunCommentRequest.body's bounds to control.MaxComment.
//
// # Input and output
//
// None: the router passes it each request.
//
// # Dependencies
//
// It imports the generated API, accessapi, iac/plan, internal/kernel and
// kernel/search, platform/httpkit's apidelta, apisearch, page and
// problem, stacksapi, and the domain's changes, control, lifecycle, logs,
// read and trigger. The runs domain's assembly constructs it.
package http
