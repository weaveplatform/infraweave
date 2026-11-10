// Package holder answers the grant views: who holds a space and the grant
// behind each. A List names a view's rows, the managed users, the managed
// user groups, the API keys but personal ones, or the users and keys with
// live sessions, and Fields what each view's search filters and orders
// by. Service.Search finds a list's holders from the evaluator that
// decides requests: a group holds a space when its bindings reach it, as
// authz.Holders answers, and a user, a key or a session when Explain,
// given its teams, finds a grant, so a user holds the grants of the groups
// its latest sign-in reported and a session those of the teams it signed
// in with. The store then searches the list's rows among those holders
// alone, so a page is full and its suggestions count no one else, and
// each row answers its grants, each binding once, with a session list's
// count of live sessions and the latest one's start.
//
// ref: decision 0007 § "Evaluation"
//
// # Input and output
//
// None: the access domain's pg adapter reads the candidates and searches
// the rows, and its http adapter serves the views through Service.
//
// # Dependencies
//
// It imports the authz and binding packages, internal/kernel and its
// search package, and the standard library. The access domain's adapters
// and assembly import it.
package holder
