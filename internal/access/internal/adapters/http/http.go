package http

// Server is the access domain's partial of the API's strict server. It
// serves no operation yet; internal/app embeds it beside the other
// domains' partials, so the first operation it implements shadows the
// router's 501, and an operation two domains implement fails to compile.
type Server struct{}
