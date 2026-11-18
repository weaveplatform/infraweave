package pg

// requestRepositoryIDSQL supports earlier provider UUID spellings while keeping
// opaque and numeric IDs case sensitive. The guarded cast is supported on the
// PostgreSQL floor and never attempts to parse a non-UUID identifier.
const requestRepositoryIDSQL = `CASE WHEN pg_input_is_valid(r.repository_id,'uuid') THEN r.repository_id::uuid::text ELSE r.repository_id END`
