// Package search is the reference's generic search as values every domain
// shares: Input, the predicates, full text phrase, order and page of a
// `POST …/search`; SuggestionsInput, its `…/suggestions` twin; Field, a
// searchable field of an entity and its type; and Result and Suggestions,
// what a search answers.
//
// Check and CheckSuggestions hold an input to an entity's fields before
// anything compiles it: a field the entity lacks, a constraint that does
// not apply to its field's type, an order of a field that does not order
// or a page without its cursor is an *Invalid, each FieldError a JSON
// Pointer into the body with decision 0022's reason, which the HTTP layer
// answers as 422 validation-failed. Constraints follow the field's type:
// booleanEquals for BOOLEAN, enumEquals for ENUM, stringEquals and
// stringMatches for STRING, hierarchyNode and hierarchyNodeValueEquals for
// HIERARCHY, and timeInRange and timeInLast for TIME. Predicates combine
// with AND, a constraint's values with OR.
//
// # Input and output
//
// None. It validates values in memory and does nothing else.
//
// # Dependencies
//
// It imports the standard library only, so a domain's core and api
// packages may depend on it. platform/sql/search compiles an Input to SQL
// under the caller's reachable spaces (decision 0007 § "Evaluation").
package search
