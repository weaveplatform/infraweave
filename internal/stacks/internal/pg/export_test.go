package pg

import (
	"github.com/weaveplatform/infraweave/internal/kernel"
	sqlsearch "github.com/weaveplatform/infraweave/internal/platform/sql/search"
	"github.com/weaveplatform/infraweave/internal/stacks/internal/core"
)

// StackSearch is the stack entity, which the external tests fuzz.
var StackSearch = stackSearch

// SearchScope is the scope a search of acct's stacks compiles under.
func SearchScope(acct kernel.AccountID, scope core.Scope) sqlsearch.Scope {
	return searchScope(acct, scope)
}
