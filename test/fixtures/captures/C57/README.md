# C57: runtime version observation and REST replay

The recorded upstream `policyRuntime { openPolicyAgentVersion }` response says
`v1.17.1` for the account/date in `meta.yaml`. The raw request, response,
redaction provenance and `observed` assertion remain historical evidence. An
infraweave dependency upgrade must not rewrite them.

`expect.yaml` names the `policy-runtime` REST replayer. It makes an authenticated
`GET /api/v1/policy-runtime` against the actual server and checks its reported
version against the repository's selected OPA module pin, including versioned
replacements. An unversioned local replacement cannot be passed off as a release.
The same checker is used by the existing runtime feature; no historical version
constant supplies the REST expectation.

The runtime library version is separate from supported policy languages.
`REGO_V0` and `REGO_V1` retain their existing boundary; the released v2 language
remains deliberately unsupported as documented in
[report 34](../../../../docs/research/reference/34-rego-v2-compatibility.md).
This replay adds no engine value or evaluator behavior.

| Evidence | Execution or source |
| --- | --- |
| Historical upstream observation | `TestAccCaptures/C57/recorded`, unchanged raw `response.json` and observed pointer |
| Actual current REST runtime | `TestAccCaptures/C57`, named dynamic callback and real PostgreSQL/server |
| Successful owner/member reads on both replicas | `policies/runtime.feature`: Both replicas report the OPA version actually linked from this build |
| 403 feature denial | Same feature: Switching off policies refuses runtime metadata reads |
| 401 missing credentials and 503 disabled caller | Same feature: Missing credentials and a disabled caller refuse runtime metadata |
| Malformed/missing/wrong runtime values and dependency replacements | Authored checker unit cases; these are not upstream captures or real service responses |

Focused real replay:

```sh
GOWORK=off INFRAWEAVE_ACCEPT_REQUIRE_DOCKER=1 \
  go test -v ./test/acceptance -run '^TestAccCaptures$/^C57$' -count=1 -timeout=10m
```

Both the `/recorded` and REST subtests must pass, and the REST callback prints
the verified linked version. The suite reports completed REST replays from the
callbacks that actually execute, including under a focused test selection.
The loader/registry rejects empty replay specifications and unknown or nil
named handlers before server startup. The C57 handoff regression also prevents
a return to a stale deferral; an empty call list cannot replace this replay.

[#679](https://github.com/weaveplatform/infraweave/issues/679) owns this bounded
handoff from completed #640/#645. [#656](https://github.com/weaveplatform/infraweave/issues/656)
owns the broader policy workflow evidence audit. This mapping does not mark other recorded,
deferred or later-phase captures implemented.
