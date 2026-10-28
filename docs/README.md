# Documentation

| Path | What |
|---|---|
| [`research/README.md`](research/README.md) | The research set: purpose, baseline and the document map |
| [`research/reference/`](research/README.md#document-map) | Reports 01 to 31: Spacelift's features, API, provider and clients, with citations; the data model, open questions and the review log |
| [`research/decisions/`](research/decisions/README.md) | Decision records 0001 to 0022 and their index, with each record's status |
| [`research/decisions/TEMPLATE.md`](research/decisions/TEMPLATE.md) | The template every decision record follows |
| [`research/architecture-options/`](research/architecture-options/README.md) | Five repository architectures compared; decision 0004 proposes the modular monolith |
| [`implementation/plan.md`](implementation/plan.md) | The implementation plan: build status, milestones, sequence and the phases P0 to P28 |
| [`implementation/capability-roadmap.md`](implementation/capability-roadmap.md) | The owning phase of every API tag, Terraform resource and data source, and `BillingTierFeature` value; the supersession register |
| [`implementation/definition-of-done.md`](implementation/definition-of-done.md) | What every pull request and every phase must show before they count as done |
| [`implementation/deviations.md`](implementation/deviations.md) | The register of every place infraweave departs from Spacelift, `DV-001` onwards |
| [`implementation/repo-layout.md`](implementation/repo-layout.md) | The repository tree the plan builds, with the phase that creates each part |

## Status

Decisions 0001 to 0003 are Accepted; 0004 to 0022 are Proposed and await the
maintainer's review, and the plan's phases rest on them. P0, the
specification reference, is done. P1, the served API skeleton, is in
progress under [#8](https://github.com/weaveplatform/infraweave/issues/8).
Every later phase is not started; the plan's
[build status](implementation/plan.md#build-status) is the record.
