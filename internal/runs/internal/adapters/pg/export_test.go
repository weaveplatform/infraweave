package pg

import (
	"github.com/weaveplatform/infraweave/internal/kernel"
	sqlsearch "github.com/weaveplatform/infraweave/internal/platform/sql/search"
	"github.com/weaveplatform/infraweave/internal/runs/internal/core/read"
)

// RunSearch is the run entity, which the external tests fuzz.
var RunSearch = runSearch

// SearchScope is the scope a search of acct's runs compiles under.
func SearchScope(acct kernel.AccountID, scope read.Scope) sqlsearch.Scope {
	return searchScope(acct, scope)
}
