# Capture fixtures

A capture is one exchange with Spacelift's real API, taken on an account
Spacelift has authorized for that use
([report 30, capture safety](../../../docs/research/reference/30-open-questions.md)),
redacted and checked in here. Checking it in resolves its record in
`docs/data/open-questions.json`
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
- `TestAccCaptures` (`make accept`) replays every call against
  `infraweave serve`. A capture whose expectation fails is a parity bug,
  reported with its id, not a flaky test.
