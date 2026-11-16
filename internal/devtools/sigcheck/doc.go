// Package sigcheck keeps every Go function signature that fits on one
// line of golines' limit on one line. golines wraps a line longer than
// its max-len but never joins one, so a signature it wrapped under a
// shorter limit, or one wrapped by hand, stays wrapped after the limit
// rises; sigcheck finds those and joins them.
//
// ReadLimit reads the limit from .golangci.yml, Join joins the signatures
// of one file that fit, and Walk checks or rewrites every Go file of a
// tree. A signature is a function's or a method's declaration up to its
// body's brace, a function literal's up to its brace, or an interface
// method's; one holding a comment, a string or a brace of its own stays
// as it is. Joining edits the source text in place, so the rest of each
// file stays byte for byte until gofmt realigns its trailing comments.
//
// # Input and output
//
// ReadLimit reads golines' max-len and tab-len from a golangci-lint
// configuration. Walk reads each Go file under its root but *.gen.go,
// generated files and testdata, vendor, node_modules, .git and .claude,
// writes back each file whose signatures it joins when asked to, and
// answers the signatures that fit, each with its file, line and joined
// length. It touches no network.
//
// # Dependencies
//
// It imports go/ast, go/parser and go/format to find and settle the
// signatures, and yaml.v3 to read the configuration. tools/sigcheck runs
// it: make sig-check, which make lint and CI's lint job run, fails on any
// signature that fits, and make sig-join joins them.
package sigcheck
