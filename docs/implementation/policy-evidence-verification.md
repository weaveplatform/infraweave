# Policy evidence API implementation

Ticket [#662](https://github.com/weaveplatform/infraweave/issues/662) owns the six
sample, capture, record-search and run-receipt evidence operations. The protected
readers and their full acceptance gate remain in progress on this branch.

## Evaluation filter snapshots

The evaluation transaction snapshots an allowlist from the actual per-policy
document: PR ID, push branch, run type, drift detection, module-event/identity,
stack name and stack repository. It skips allocating provider values and other
private document subtrees. Nullable fields distinguish unavailable/null facts
from known false, zero or empty values. The metadata contains no source, input
document, raw result, print or object-store key beyond the existing private
sample reference owned by persistence.

These values are recorded even when sampling is false. Metadata queries can
therefore filter without decrypting retained input or consulting mutable current
run/stack facts. The real PostgreSQL/OPA test changes its fixture's current stack name
after evaluation and verifies the original input facts, known-false drift,
absent sample link and zero object/budget rows. Typed projection tests cover
push/PR, run and module notifications, unavailable facts and private-value
exclusion.

C273 records the PLAN suggestion names `createdAt`, `outcome`,
`runDriftDetection`, `runType`, `stackName` and `stackRepository`. Actual records
also establish lowercase `allow` and `approve` outcomes. Remaining lowercase
outcome mappings and the push/notification names are infraweave implementation
choices, not additional observed Spacelift facts. Notification metadata records
both a nullable module-event Boolean and an optional module ID, keeping that
choice explicit rather than inferring an upstream module-filter type. The shared
search engine's TIME/filterable behavior is likewise a platform normalization;
C273's createdAt field is orderable but not filterable.

The capture read projection declares `input` required and nullable, ensuring a
superseded capture can return explicit `input: null` instead of omitting it.
Root-space administration, current entity ownership, bounded authenticated
reads, safe failure auditing and all six operations' real HTTP journeys remain
required before this ticket is complete.
