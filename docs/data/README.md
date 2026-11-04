# Reference data

The registers in this directory are infraweave's lookup data: records that
documents, tests and tools find by ID. Markdown elsewhere in `docs/` explains
and guides; it cites a register's records by ID and never repeats them as a
table, so each fact has one copy.

## Registers

| File | Holds | Read by |
|---|---|---|
| [`renames.json`](renames.json) | Reference names infraweave serves or reads under another name ([0001](../research/decisions/0001-rest-first-spacelift-surface.md#rename-table)) | `TestUnitServedNames` |
| [`problems.json`](problems.json) | Every problem type of the served APIs ([0003](../research/decisions/0003-api-conventions.md#problem-types), [0022](../research/decisions/0022-api-errors-and-update-semantics.md)) | `TestUnitAPIProblemTypes` |
| [`features.json`](features.json) | The feature catalogue, its one source: every capability, option, limit and the `billing` switch, with lowest seeded tier, limit values, requirements and owning phase ([0021](../research/decisions/0021-capability-scope-and-supersession.md#the-feature-catalogue-pd-05)) | `make generate` (`internal/devtools/dataview`, and `internal/features/catalogue.gen.go` once P3b builds it); docscheck's phase check |
| [`capabilities.json`](capabilities.json) | Every reference tag, untagged operation, Terraform resource and data source and `BillingTierFeature` value, with report 26's research (`spacelift`) and infraweave's feature, tier, phases and state (`infraweave`) ([26](../research/reference/26-scope-and-tiers.md)) | docscheck's tag and phase checks; `make generate` (`internal/devtools/dataview`); `specref parity --states --check` once built |
| [`supersessions.json`](supersessions.json) | Interfaces left out as superseded (`SUP-`) and deprecated ones still served (`DEP-`) ([0021](../research/decisions/0021-capability-scope-and-supersession.md#the-supersession-test)) | docscheck's phase check and citations |
| [`deviations.json`](deviations.json) | Every deliberate departure from Spacelift, `DV-001` onwards, with its group, client impact and deciding record ([deviations.md](../implementation/deviations.md)) | docscheck's citations of `DV-NNN` |
| [`run-transitions.json`](run-transitions.json) | Every edge of the run state machine: report 08's T1 to T43 and decision 0008's T44 ([08](../research/reference/08-run-lifecycle.md#transition-table), [0008](../research/decisions/0008-run-execution-and-worker-protocol.md#the-state-machine-is-the-authority)) | docscheck's citations; `TestUnitTransitionRegister` once P7 builds it |
| [`open-questions.json`](open-questions.json) | Report 30's open questions: product decisions (`PD-nn`), captures needed (`Cn`) and re-verify triggers (`RV-n`), each with the reports that cite it ([30](../research/reference/30-open-questions.md)) | docscheck's citations and *(unverified)* check |
| [`review-findings.json`](review-findings.json) | Every finding of the fact-check passes, with its status and the phrases that show its change still holds ([31](../research/reference/31-review-log.md)) | docscheck's evidence check |
| [`graphql-inventory.json`](graphql-inventory.json) | Every named type of Spacelift's GraphQL schema: fields with arguments, types, nullability, deprecation and input defaults, enum values and union members ([29](../research/reference/29-data-model-inventory.md)) | `make verify-reference` (`specref inventory --check`) |

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
