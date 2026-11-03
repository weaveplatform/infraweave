# Documentation

| Path | What |
|---|---|
| [`../GLOSSARY.md`](../GLOSSARY.md) | infraweave's own language: the terms its docs and code use, and the words to avoid |
| [`data/`](data/README.md) | Reference data: the registers that documents, tests and tools look up by ID, each validated by its JSON Schema |
| [`research/README.md`](research/README.md) | The research set: purpose, baseline and the document map |
| [`research/reference/`](research/README.md#document-map) | Reports 01 to 31: Spacelift's features, API, provider and clients, with citations; the data model, open questions and the review log |
| [`research/decisions/`](research/decisions/README.md) | Decision records 0001 to 0024 and their index, with each record's status |
| [`research/decisions/TEMPLATE.md`](research/decisions/TEMPLATE.md) | The template every decision record follows |
| [`research/architecture-options/`](research/architecture-options/README.md) | Five repository architectures compared; decision 0004 adopts the modular monolith |
| [`implementation/plan.md`](implementation/plan.md) | The implementation plan: build status, milestones, sequence and the phases P0 to P28, with the organization level in P3 and P3b, Flows in P18c and P18d and the console in P24c |
| [`implementation/capability-roadmap.md`](implementation/capability-roadmap.md) | How to read the feature catalogue, the reference rows and the supersession register that `data/features.json`, `data/capabilities.json` and `data/supersessions.json` hold |
| [`implementation/tiers.md`](implementation/tiers.md) | Generated from the registers: every feature against the seeded tiers, the seeded limits and the reference rows each phase owns |
| [`implementation/definition-of-done.md`](implementation/definition-of-done.md) | What every pull request and every phase must show before they count as done |
| [`implementation/deviations.md`](implementation/deviations.md) | What counts as a departure from Spacelift and how to record one; the records, `DV-001` onwards, are in [`data/deviations.json`](data/deviations.json) |
| [`implementation/repo-layout.md`](implementation/repo-layout.md) | The repository tree the plan builds, with the phase that creates each part |

## Status

Decisions 0001 to 0024 are Accepted, and the plan's phases build them; a
decision changes by amendment in place. P0, the
specification reference, is done. P1, the served API skeleton, is in
progress under [#8](https://github.com/weaveplatform/infraweave/issues/8).
Every later phase is not started; the plan's
[build status](implementation/plan.md#build-status) is the record.
