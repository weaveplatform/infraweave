# Policy evidence API verification (#662)

The branch implements all six policy/run evidence operations. It depends on
#661's sealed retention and does not mark the complete Policies tag implemented.
Full final-head CI and merge remain required before ticket closure.

## Ownership and raw reads

Policy samples bind the exact account, current policy and evaluation handle.
Run samples first hold the owned stack, run and exact immutable receipt link,
then call the policies-owned reader. Current policy-space read permission and
root-space SPACE_ADMIN are required before a sealed input or print is opened.
Child-space administration confers no raw evidence permission. Handles are opaque
evaluation/capture IDs; no request accepts a blob path or returns a signed URL.

The outer transaction disables retries and holds owner/blob deletion fences
through a 30-second, 128-MiB authenticated read. Runs hold stack → run → receipt
before policy → blob. Captures probe immutable ownership, hold/authorize its
space, then lock head → input → blob, matching evaluation/publication order.
Expired, absent, foreign and unpublished samples are not found. A superseded,
previously published capture returns its summary with required `input: null`;
an absent head or expired input is not a replacement. Raw read failures commit
only a fixed classification and actor/account into the audit trail after the true
outer transaction has ended, including the runs-owned fence. Backend error text,
input, source, object keys and print are excluded from that record.

## Safe metadata

Eight nullable columns in the initial policy migration snapshot the actual
per-program OPA document: PR ID, push branch, run type, drift detection, module
event/ID and stack name/repository. Missing/null differs from known false, zero
or empty. No private plan values, raw decision, source or print enter them.
Search/suggestions apply account and requested policy before counts/pagination;
they join the current policy space rather than granting historical-space access.
They select no input/staging/blob content and work before any upload runs.

C273 records PLAN fields `createdAt`, `outcome`, `runDriftDetection`, `runType`,
`stackName` and `stackRepository`, and records lowercase `allow`/`approve`.
Other lowercase outcomes and these additional fields are explicit infraweave
choices, not new observed Spacelift facts: push `pullRequestId` (STRING), `branch`
(STRING), notification `module` (BOOLEAN), `moduleId` (STRING), run/drift and stack
fields. Shared search TIME/filterability is a platform normalization: C273's
createdAt is orderable but not filterable. The module notification input used in
storage verification is labelled a contract fixture, not a live module capture.

## Real proof

- Public BDD: all six operations through A/B with actual PostgreSQL, Gitea,
  RustFS-sealed OPA evidence, root success, unbound concealment, child-admin raw
  denial, another account's isolation, typed-filter/cursor failures, expiry and
  public sampleExpired flags. A real server restart with policies disabled proves
  all six owned routes return `403 feature-disabled`. This is 40 passing steps.
- The empty owned capture collection returns `200` and `items: []` (5 steps).
- Real PostgreSQL/sealed-object tests prove exact parent binding, safe audits
  after rollback, object-independent metadata, policy movement, typed PLAN,
  push/PR and run/module notification filters, and replacement across independent
  DB-backed readers. The module document is a synthetic typed-contract fixture.
- Independent PostgreSQL transactions prove a held run/receipt link blocks
  deletion, rejects wrong account/stack/run/receipt tuples and releases the fence
  on completion. No deadline, retry, quota, coverage or rate limit is relaxed.

`GOFIPS140=certified GODEBUG=fips140=only`, race detection and shuffled order are
used for focused storage/owner proofs. Final generated/reference, configured lint,
all required CI coverage and full certified-FIPS acceptance remain merge gates.
