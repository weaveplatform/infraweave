// Package apisearch converts the reference's generic search between the
// API's shapes and kernel/search, for every domain that serves a search:
// Input and SuggestionsInput read a SearchInput and a
// SearchSuggestionsInput, Suggestions writes a SearchSuggestionsOutput,
// in which every field filters and a field's values are listed only when
// asked for, and PageInfo writes a page's cursors and neighbours. A list
// present but empty stays set, so a constraint of no values keeps no row.
//
// # Input and output
//
// None: it converts values in memory.
//
// # Dependencies
//
// It imports kernel/search and platform/gen/api. The HTTP adapters of the
// domains that serve searches import it.
package apisearch
