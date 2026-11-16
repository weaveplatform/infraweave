package pg

import (
	"github.com/weaveplatform/infraweave/internal/kernel"
	sqlsearch "github.com/weaveplatform/infraweave/internal/platform/sql/search"
	"github.com/weaveplatform/infraweave/internal/state/internal/core"
)

// WorkspaceSearch is the workspace entity, which the external tests fuzz.
var WorkspaceSearch = workspaceSearch

// WorkspaceSearchScope is the scope a search of acct's workspaces
// compiles under.
func WorkspaceSearchScope(acct kernel.AccountID, scope core.WorkspaceScope) sqlsearch.Scope {
	return workspaceScope(acct, scope)
}

// VersionSearch is the version entity, which the external tests fuzz.
var VersionSearch = versionSearch

// VersionSearchScope is the scope a search of scope's versions compiles
// under.
func VersionSearchScope(scope core.VersionScope) sqlsearch.Scope { return versionScope(scope) }
