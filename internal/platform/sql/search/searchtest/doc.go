// Package searchtest holds an entity's compiled searches to the rules of
// platform/sql/search: Check draws two searches of one shape from a byte
// string, of every shape the entity's fields allow, and checks that their
// values only ever reach the query as parameters and that every query
// holds its rows to the reachable spaces. FuzzCompile runs Check from
// Seeds and the fuzzer's inputs; a domain's pg adapter runs it over each
// searchable entity it defines.
//
// ref: decision 0007 "Evaluation"
//
// # Input and output
//
// None: it compiles queries in memory and runs none. Check answers a
// query that breaks a rule as ErrUnsafe.
//
// # Dependencies
//
// It imports testing, internal/kernel, kernel/search, platform/sql/search
// and platform/httpkit/page for cursors. Only tests import it.
package searchtest
