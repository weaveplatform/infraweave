# Reference data

The registers in this directory are infraweave's lookup data: records that
documents, tests and tools find by ID. Markdown elsewhere in `docs/` explains
and guides; it cites a register's records by ID and never repeats them as a
table, so each fact has one copy.

## Registers

| File | Holds | Read by |
|---|---|---|
| [`renames.json`](renames.json) | Reference names infraweave serves or reads under another name ([0001](../research/decisions/0001-rest-first-spacelift-surface.md#rename-table)) | `TestUnitServedNames` |
| [`problems.json`](problems.json) | Every problem type of the served APIs ([0003](../research/decisions/0003-api-conventions.md#problem-types), [0022](../research/decisions/0022-api-errors-and-update-semantics.md)) | `TestUnitDeployProblemTypes` |

## Shape

A register is `docs/data/<name>.json`:

```json
{
  "$schema": "schema/<name>.schema.json",
  "records": [{"id": "...", "...": "..."}]
}
```

Its schema, `schema/<name>.schema.json`, is JSON Schema 2020-12 and may
`$ref` the shared definitions in `schema/common.schema.json`. Text members
hold markdown, and their relative links start from this directory. A schema
also carries keywords that say how the rest of the tree refers to the
register:

| Keyword | Meaning |
|---|---|
| `x-id-pattern` | Required. Every record's `id` matches it in full |
| `x-cite-pattern` | Finds a citation of one of the register's IDs in markdown under `docs/` and in other registers; every match must be a record |
| `x-cited-in` | A record member listing repository files, each of which must cite the record's ID |
| `x-evidence` | A record member holding `{file, present, absent}`: phrases that must and must not appear in that file, whitespace compared as single spaces |

## Rules

- **IDs are stable.** An ID is never reused or renumbered; a record that no
  longer holds is kept with a status that says so, where its schema has one.
- **One copy.** A fact a register holds is not restated as a table in
  markdown. Prose may name a record by its ID and say what it means for the
  reader at that point.
- **Checked.** `make docs-check` validates every register against its
  schema, rejects duplicate IDs, checks the links in its text, every cited
  ID, every `x-cited-in` file and every `x-evidence` phrase.
