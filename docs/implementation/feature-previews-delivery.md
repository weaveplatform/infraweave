# Feature preview delivery

Delivery ticket: [#825](https://github.com/weaveplatform/infraweave/issues/825).
Epic: [#820](https://github.com/weaveplatform/infraweave/issues/820).
Contract: [0021](../research/decisions/0021-capability-scope-and-supersession.md#feature-previews).
The companion [machine-readable matrix](feature-previews-delivery.json) records
scenario names, source lines, step counts and revisions.

## Executed baseline

Revision `2004d821e0afd7f2b6a44ff4b3000bb34973c943` has a successful
[quality gate](https://github.com/weaveplatform/infraweave/actions/runs/45817912926).
Its eight `cover-accept-*` artifacts contain 226 features / 1,325 scenarios.
The preview API feature executes 6 scenarios / 88 steps; the CLI feature
executes 5 scenarios / 57 steps. Every step in those features passes.
The gate includes all three OS unit jobs, lint, acceptance, certified FIPS,
generated-code checks and parity. Coverage is 95.3% (60,275 / 63,258 statements),
with every gated package meeting 90%.

Local execution at that same revision independently passes the API and CLI
features with those same counts against real server subprocesses and PostgreSQL.
`TestUnitPreviewGate` (domain and CLI), `TestAccPreviewSettings` and
`TestAccPreviewGateBilling` pass in ordinary and certified FIPS mode. The native
PostgreSQL tests exercise cross-replica notification, opt-in and opt-out, billing
precedence, member removal, account purge and organization purge.

## Operation matrix

| Operation | Success | Literal 403 denial | Failure |
|---|---|---|---|
| `featurePreview` | Empty catalogue, account opt-in and personal settings read across replicas | `feature_previews_disabled.feature`: installation disables `account-settings` | Unadmitted member's session returns 401 |
| `accountSetFeaturePreviewsEnabled` | Account admin opts in and out across replicas | Non-admin member and API key return `forbidden`; installation switch returns `feature-disabled` | Invalid type, unknown/missing field return 400; idempotency-key reuse returns 422; unadmitted session returns 401 |
| `featurePreviewSetPersonalEnabled` | Browser session and personal key opt out and back in across replicas | Key without a member returns `forbidden`; installation switch returns `feature-disabled` | Invalid type/unknown field return 400; unadmitted session returns 401 |

The baseline's denial-tagged scenarios read `featurePreview` successfully while
refusing writes. Tags alone therefore do not prove a 403 on the read. The
additional disabled-settings feature closes that gap for all three operations.
At revision `c5ffaa26cc0155fa45cfb2e0da8641054311d535` it passes together with
the original API feature under certified FIPS: 7 scenarios / 99 steps, all
passed (including the new scenario's 11 steps). The companion matrix records
the results.

## Scope and gates

No catalogue feature currently carries a `preview` marker. The real API reads
`features: []` and `visible: false`. The `preview-not-enabled` path is proven by
unit tests over a test catalogue, including billing off/on, disabled-feature and
tier precedence; native PostgreSQL tests also exercise that test catalogue.
A first real preview must add its own acceptance scenario. A personal opt-out
changes client visibility and refuses no server operation. No live Spacelift
capture is claimed.

`tag:Account Settings` stays `partial`: these three operations do not finish the
other settings owned by later phases. None of these operations is in the parity
baseline's missing list. `specref parity --states --check` passes locally when
supplied the complete baseline's merged Cucumber results; a focused report alone
cannot evidence unrelated capability states. The parity baseline needs no edit.

The successful baseline CI gate supplies the OS/FIPS/coverage evidence above.
A full local `make gate` run is not part of this evidence.
