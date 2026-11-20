# Capture fixtures

A capture is one exchange with Spacelift's real API, taken on an account
Spacelift has authorized for that use
([report 30, capture safety](../../../docs/research/reference/30-open-questions.md)),
redacted and checked in here. A complete observation resolves its record in
`docs/data/open-questions.json`; a partial capture or a real entitlement refusal
keeps its missing cases open and names them in the fixture README
([decision 0016, capture fixtures](../../../docs/research/decisions/0016-testing-and-acceptance.md#capture-fixtures)).

| Path | Content |
|---|---|
| `C<n>/meta.yaml` | `entry` (the register record, which names the directory), `captured` (YYYY-MM-DD), `tier`, `introspection` (`sha256:` of the account's introspection result), `capturedBy`, `redacted` (what was replaced) |
| `C<n>/request.graphql` | the GraphQL request as sent, redacted |
| `C<n>/response.json` | the response as received, redacted |
| `C<n>/expect.yaml` | `calls`: the infraweave REST calls that stand for the exchange, each with `method`, `path`, optional `headers` and `body`, and `expect`: `status`, optional `problem` (a slug of `docs/data/problems.json`) and `body` (JSON Pointer to value) |

Both YAML files are decoded strictly: an unknown key fails.

`C0` is a synthetic example, marked `example: true`. It is validated and
replayed like a capture, but records nothing Spacelift did, and it counts
as no capture.

- `TestUnitCaptureRedaction` fails on anything here that looks like a JWT,
  an API key secret, an AWS access key, a private key block, or an e-mail
  address outside `example.com`.
- `expect.yaml` may instead contain `observed` (JSON Pointer to recorded value)
  and `replayAfter` (the implementation issue). Such a capture has no REST calls:
  offline observations are checked now, and REST replay is explicitly deferred.
  Once implemented, remove `replayAfter` and add calls or `replayWith`, a named
  stateful REST replayer. C49 intentionally expects
  a different infraweave default (V1); C57 must check the linked OPA version rather
  than freezing the reference service version.
- A partial handoff uses `replaySubset` and `handoffs`, keyed by exactly the
  recorded `observed` pointers. Each handoff needs `classification` (`rest`,
  `deferred`, `upstream`, `inference` or `deviation`), `owner` and `detail`. Both
  executable REST assertions and remaining handoffs are required. The subset
  handler must return exactly the REST assertion pointers it executed. Missing,
  unknown or empty handlers fail; subsets count separately from full REST
  replays. C48 demonstrates this without converting its partial workflow capture
  into a complete one. Subsets cannot mix with calls, full replayers or blanket
  deferral.
- `TestAccCaptures` (`make accept`) verifies the recorded policy observations and replays every enabled call against
  `infraweave serve`. A capture whose expectation fails is a parity bug,
  reported with its id, not a flaky test.

The 2026-11-19 workflow lab used one stack, ten policies and four provider-free
runs. The final live inventory contains no stacks or policies. Its temporary
GitHub source branch was deleted and the CLI verified a 404 response. The public
fixture source was commit `46ac93f71e8ae479cb0ef03ae5ad835c48fb74dd`; no plan was
applied. Each group retains its actual request/response sequence and cleanup.

| Capture | Observed | Missing evidence | REST replay |
|---|---|---|---|
| C9 | Attached ordinary policy refuses deletion without force; force deletes it and its attachment | Context and plugin-managed deletion | #645 |
| C46 | Real custom-ignore and default pushes; ignored-history query refused on V5_FREE | Entitled-account reason codes and disabling label | #652 |
| C48 | Free-tier creation/attachment differences and actual PLAN/APPROVAL/INITIALIZATION evaluation | Complete type mapping with an entitled-account comparison | #683 executes management/attachment subset; #650/#651/#654 own remaining workflow; #656 audits evidence |
| C51 | New slug-ID policy successfully resets its own flag; historical receipt remains | Legacy internal-ID eligibility/migration and ULID-ID comparison | #649 |
| C56 | Unix seconds for create/update; createdAt remains fixed | None | #645 |
| C291 | Mixed-policy rejection fails UNCONFIRMED and PENDING_REVIEW with the exact note | None | #650 |

C56 and C291 resolve their register questions. The partial cases remain open;
their fixtures verify the evidence or refusal. The maintainer separately accepted
ADR 0009's labelled inferences for C46/C48/C51 on 2026-11-19, lifting their
implementation gates while keeping the evidence questions open. A CI pass alone
would not establish a missing upstream observation.
