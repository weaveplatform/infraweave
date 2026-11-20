# C49: omitted policy engine

The recorded upstream create omits `engineType` and returns `REGO_V0`. That
observation remains unchanged in the response and `observed` expectations.

The maintainer intentionally chose `REGO_V1` for infraweave on 2026-11-19,
recorded in ADR 0009. `TestAccCaptures/C49` exercises that approved deviation
through real authenticated REST creation with the engine omitted, verifies V1
is persisted on read, then deletes the policy and verifies cleanup. The normal
two-server management matrix also checks the V1 default and preserved V0 updates.
