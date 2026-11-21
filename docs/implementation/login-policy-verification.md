# LOGIN admission verification

Implementation: [#658](https://github.com/weaveplatform/infraweave/issues/658),
under [P13b #636](https://github.com/weaveplatform/infraweave/issues/636).
The complete phase gate remains [#666](https://github.com/weaveplatform/infraweave/issues/666).

## Admission and session ownership

Organization authentication supplies original membership, teams and IdP facts.
Account entry evaluates every effective root LOGIN policy through the actual
policy owner and OPA runtime. Unknown facts remain unknown; managed membership
does not manufacture upstream membership. LOGIN output supplies effective
session permissions and teams without rewriting original identity facts.

An account fence orders admission with scheme/policy changes, key mutations and
revocation. The verified key or exact source session must still be usable before
the new session commits. Grants belong to that account and exact session ID;
account switches, CLI sessions and OAuth grants do not inherit another SID's
LOGIN grants or SCHEME_SWITCH exception. The root API-key exception requires
that key's own live root IAM admin binding. It does not make an ended session
usable again.

The access-holder views use that same exception. Under LOGIN, the key with its
own root IAM admin binding remains visible; an ordinary IAM reader does not.
The switching key's session exposes both `ASSIGNED_ROLE` and `SCHEME_SWITCH`
while its root binding exists. Removing that binding removes the key from
both replicas' key-holder views and ends a separately exchanged session's
administration. Only the exact switching session retains `SCHEME_SWITCH`.

Accepted LOGIN policy creation, update and deletion revoke peer sessions in the
same transaction, retain the exact editor's existing grants, bump authorization
version and invalidate caches after commit. Inactive configuration and failed
compilation leave managed sessions alone. Removing a custom role cascades only
its ephemeral session grants; existing IAM binding restrictions remain.

OAuth consent snapshots the exact browser's original facts atomically with its
code. Redemption reevaluates current account admission, installs independent
grants and spends the code together. A refusal commits evaluation metadata
without a grant or code consumption. The browser may log out after consent.
Refresh rotates the same SID and retains LOGIN's frozen grants and effective
teams; managed-user refresh still reads managed groups. The final consent write
uses wall-clock expiry so a lock wait cannot extend an expired request/code.

## Public acceptance matrix

The `@policy-login` matrix uses real servers, PostgreSQL, signed tokens and,
for the review case, Gitea. It contains sixteen scenarios:

| Cases | Evidence |
| --- | --- |
| API key entry: 3 | Denial on both replicas, effective scoped grants and the root IAM key exception |
| Member entry: 5 | A admits/B refuses, private refusal detail hidden, empty account switcher, CLI reentry and true/false/unknown original-membership fallback |
| Policy changes: 3 | Active creation/update/deletion revoke peers across replicas; editor/other account survive; inactive and invalid edits preserve managed sessions |
| OAuth: 2 | Unmanaged organization member consents, logs out, redeems and refreshes; original/effective teams, exact grants and stable SID checked; changed policy refuses without spending the code |
| Public review: 1 | An unmanaged LOGIN member's actual identity and session reach an APPROVAL program and release the queued run |
| Runtime failures: 2 | Malformed REGO_V0 output and an expensive REGO_V1 program reaching the actual OPA deadline commit RESULT/TIMEOUT evidence without sessions, grants or run flags |

The existing `auth/oauth_server.feature` additionally retains all four managed
OAuth scenarios (83 steps), including remote redirect controls, code/refresh
replay, explicit denial and managed-member removal.

Run the public matrix with:

```sh
GOWORK=off GOFIPS140=v1.0.0 GODEBUG=fips140=only \
  INFRAWEAVE_ACCEPT_REQUIRE_DOCKER=1 INFRAWEAVE_ACCEPT_TAGS=@policy-login \
  go test -count=1 -timeout 10m -run '^TestAccFeatures$' ./test/acceptance
```

Real PostgreSQL tests separately verify authorization-write notifications,
transaction rollback and cache invalidation, role deletion, a credential changed
after verification, consent expiring while an account lock is held, and refusal
of ended-session review metadata. A timing evaluator used only by the storage
write audit is explicitly identified; it is not OPA acceptance evidence.

Storage-boundary controls also refuse mismatched or ended identity sources,
preserve consent codes on replay/refusal, and verify a legitimate account switch
within the organization. Real PostgreSQL backend termination at ten LOGIN
mutation boundaries must roll back peer revocation, grant/version changes and
cache invalidations. Entry, account discovery and consent retain unavailable
error classification without partial results. The missing-runtime check proves
safe storage wiring failure; it does not substitute for the public OPA matrix.

## Evidence limits and final gate

These are authored infraweave acceptance programs. The identity-session harness
does not claim a live organization IdP sign-in, which belongs to its later
phase. C50's unavailable LOGIN flag observations remain in the capture register;
the maintainer-approved no-run flag behavior is an implementation inference,
not a fabricated upstream response.

The focused acceptance and PostgreSQL checks do not complete #658 by themselves.
Its exact PR revision must also pass all required CI, including the unchanged
95% total / 90% per-package coverage and certified FIPS gates. P13b's remaining
tooling and integrated phase proof stay with their own tickets and #666.
