// Package search compiles the reference's generic search (kernel/search)
// to parameterized PostgreSQL for any entity that describes its fields.
//
// An Entity maps each searchable field to a Column: its SQL expression,
// its type, whether it is an array, whether the full text phrase searches
// it and, for a HIERARCHY field, the subquery templates that expand a node
// to its descendants and list the nodes. Find reads a page of the rows a
// Scope allows: the caller's own condition, and Space = ANY($n) over the
// spaces Reachable holds, which every compiled query ANDs before any
// predicate, so the database never returns a row the caller may not see
// and a page before the last is always full (decision 0007 §
// "Evaluation"). Every value reaches the query as a parameter; only the
// entity's own SQL is ever written into it. Pages are keyset reads in the
// search's order, then the entity's ID; FIRST and NEXT read forwards,
// PREVIOUS and LAST backwards. A cursor (decision 0003) holds the last
// row's keys as text, cast back to each column's type, and names the
// entity and the order, so a cursor of another list or order, or one a
// client altered, answers 400 invalid-cursor. Suggest counts the rows the
// same filter keeps and lists the most common values of each field asked
// for, with counts, so no value or count reaches past the reachable
// spaces.
//
// # Input and output
//
// Find and Suggest run queries through the tx.Querier they are handed and
// nothing else. An input the entity cannot answer is a
// *kernelsearch.Invalid, which the HTTP layer answers as 422
// validation-failed with a pointer to each field; a query that fails
// because PostgreSQL cannot be reached is marked kernel.ErrUnavailable.
//
// # Dependencies
//
// It imports pgx, internal/kernel and kernel/search for the values,
// platform/tx for the querier, platform/sql for Unavailable and
// platform/httpkit/page and problem for cursors. A domain's pg adapter
// imports it for each searchable entity.
package search
