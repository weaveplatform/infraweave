package search_test

import (
	"testing"

	"github.com/weaveplatform/infraweave/internal/platform/sql/search"
	"github.com/weaveplatform/infraweave/internal/platform/sql/search/searchtest"
)

// FuzzCompile holds the test entity, which has a column of every type, to
// parameterized searches.
func FuzzCompile(f *testing.F) { searchtest.FuzzCompile(f, search.Items, search.ItemsScope) }
