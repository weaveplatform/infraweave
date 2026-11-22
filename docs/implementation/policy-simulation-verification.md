# Edited policy simulation

Implementation ticket: [#663](https://github.com/weaveplatform/infraweave/issues/663),
under [P13b](https://github.com/weaveplatform/infraweave/issues/636).

`POST /api/v1/policies/simulate` compiles submitted source with the same OPA
runtime, preamble, capabilities, namespace rules and prepared-query cache as
policy management and live evaluation. It then evaluates the edited JSON with
the simulation input bound. Explicit REGO_V0 and REGO_V1 are supported; omission
intentionally selects V1 as policy creation does. The result is the full decision
encoded as a JSON string, preserving the recorded `policySimulatev2` contract.

The simulator calls the runtime directly. It does not select stored policies,
invoke the live evaluator, apply a decision, emit evaluation events or call the
sample owner. A true `sample`, LOGIN administrator grant or target decision in
the returned document has no persistence or workflow effect. Shared HTTP
authentication, authorization, rate limits and optional idempotency controls
still apply independently of this domain behavior.

The operation requires the policies capability and SPACE_READ in at least one
account space. A reader can evaluate their own submitted text without gaining
access to stored inputs or other private evidence. Compiling user-supplied code
grants no session or administrative permission. `exec`, removed built-ins and
foreign package names are refused by the actual compiler. Nonempty library
requests are refused explicitly because this runtime does not support policy
libraries; they are never silently ignored.

Source is limited to 256 KiB and the decoded JSON input string to 4 MiB, even if
an operator raises runtime limits. Lower configured limits continue to apply.
The default shared HTTP envelope is 1 MiB, so larger inputs also require an
operator to raise `server.max_body_bytes`. The boundary tests use a bounded
32 MiB envelope to accommodate JSON escaping; the simulator does not bypass the
shared HTTP limit. Compilation and evaluation retain the configured deadline
for the submitted policy type. Invalid source/input/results and exhausted
budgets return validation problems, with source ranges for compile diagnostics.

The response contract exposes the decision string, without print fields. The
runtime captures print in its bounded memory buffer and does not log it. The
simulation endpoint does not add print keys to the recorded decision document,
persist diagnostics as a sample, or reuse print from an earlier request.

## Executable evidence

The initial public-endpoint baseline returned 405: POST was absent even though
the dynamic policy item path accepted GET. The final authored features in
`test/acceptance/features/policies/simulation*.feature` pass **22 scenarios /
158 steps** against two actual servers, PostgreSQL, S3-compatible object storage
and Gitea where required, with certified FIPS enabled. They cover both dialects,
omitted V1, denied space access, feature switch-off, missing authentication,
invalid JSON/Rego/dialects, disabled built-ins, exact byte boundaries, the HTTP
envelope and a real OPA deadline.

Recorded preamble, LOGIN role forms, TRIGGER defaults, C273's simulation and
C192's `sanitized()` vectors are replayed through the public endpoint, comparing
the complete decision. Only the documented package/input namespace is mapped;
the source recordings remain unchanged. The captured legacy ACCESS form is
explicitly refused by the served enum, consistent with ADR 0009. C192 now has
an executable `policy-simulation` capture replay instead of a ticket deferral;
its focused real REST replay passes independently.

The combined capture pass also starts with C192 before C291, C46, C48 and
C51. C192 must register the common workflow steps when it initializes the
shared replay suite. Without them, the later captures cannot check their
restored inventories. The same real-service sequence fails before the fix and
passes **7 scenarios / 305 steps** afterward with certified FIPS, including
all rejection, reset and inventory cleanup checks.

Before/after tests compare policy/evaluation/capture/quota/receipt/flag rows,
run state/review/gate rows and actual S3 object inventories. An additional case
first creates a real sampled TASK denial, waits for its sample and sanitized
capture to publish, then simulates a true-sample LOGIN administrator grant as a
reader. Existing evidence remains byte-for-byte unchanged, the reader still
receives 403 for administrative space creation, and the print marker is absent
from server output. These are authored implementation tests; they do not claim
unobserved vendor side effects.

The complete policy suite, shared API contract tests, generation, reference,
lint and repository-wide coverage/FIPS CI remain required before merge.
