# 0019: AI provider boundary

Status: Proposed

## Context

Spacelift Intelligence is four parts on one toolset: AI integrations (stored
connections to a model provider), Infra Assistant (the in-product chat, with
run summaries), Intent (natural-language provisioning inside governed
projects) and a remote MCP server at `/mcp`
([22 section 1](../reference/22-intelligence.md#1-the-parts-of-spacelift-intelligence)).
All of it is in scope ([0021](0021-capability-scope-and-supersession.md)).

infraweave has to decide where the boundary to model providers sits and
answer two product decisions from
[30](../reference/30-open-questions.md#product-decisions):

- **P10.** Does infraweave mirror the standalone `spacelift-intent` stdio
  server, the hosted `/mcp` Intent tools, or both?
- **P11.** Bring-your-own-model only, as self-hosted Spacelift requires, or
  also an operator-configured default integration with a usage budget, as
  SaaS offers?

The facts that constrain the answer:

- On SaaS, a Spacelift-provided integration serves Claude models on
  Bedrock, with a USD 10 per 30-day budget on the Free tier and none on
  paid tiers; customers may add their own Anthropic, OpenAI, Google or
  Bedrock integrations
  ([22 section 3.2](../reference/22-intelligence.md#32-providers-and-models),
  [section 2.2](../reference/22-intelligence.md#22-tier-and-release-status)).
- Self-hosted Spacelift has no provided integration, so it requires BYOM
  ([26 section 2.3](../reference/26-scope-and-tiers.md#23-self-hosted)).
- `spacelift-intent` is archived, and its README sends users to the managed
  version for policies and auditing
  ([01 section 5](../reference/01-spacelift-landscape.md#5-the-spacelift-io-repositories),
  [22 section 8](../reference/22-intelligence.md#8-the-open-source-spacelift-intent)).
- No GraphQL operation sends a chat message; the chat transport is
  unpublished (capture C217).
- infraweave's API is REST (decision 0001), while Spacelift's MCP tools
  `discover`, `query` and `mutate` are GraphQL-shaped
  ([22 section 7.2](../reference/22-intelligence.md#72-tools)).

## Decision

### P10: hosted `/mcp` tools only

infraweave serves the hosted MCP surface at `/mcp` and does not reproduce
the standalone `spacelift-intent` server or its 18 tools. Local clients that
speak only stdio use `infraweavectl mcp`, a client CLI subcommand that proxies stdio
to `/mcp` with the CLI's credentials (decision 0006) and adds no tools of
its own. Under 0021's rule the standalone server is superseded: it is
archived, its replacement is named by its own README, the hosted `intent`
tool covers its use case with policies, locks and audit added, and no
stored data needs migrating, since its state is a local SQLite file.

### P11: BYOM plus an optional installation default with a budget

- Customers create their own integrations (BYOM) for any of the four
  providers, with no infraweave-imposed limit.
- The operator may configure one installation-provided default
  integration under `ai.default_integration`. It appears in the API with
  `isSpaceliftProvided: true`, `space: null`, read-only except for
  `aiIntegrationToggle`, exactly as Spacelift's provided integration does.
- The default carries an optional token budget per rolling window
  (`ai.default_integration.budget.tokens`, `.window`, default `720h`);
  `0` means unlimited. `tokenUsage` reports the window. A request that
  would start once the budget is spent answers `429`, problem
  `ai-budget-exhausted`, with `Retry-After` until `tokensResetAt`;
  decision 0022 lists it.
- With no default configured, nothing is installation-provided: the list
  holds only BYOM integrations, and an operation that addresses the
  provided integration answers `403` `installation-unsupported`.
- Default: not configured.

### Provider abstraction

`internal/ai` holds one interface, and every model call in infraweave goes
through it in the `serve` role. This is the AI provider proxy that
`AIIntegration.enabled` implies
([22 section 3.1](../reference/22-intelligence.md#31-the-entity)): the
browser, MCP clients and agents never hold a provider key.

```go
type Provider interface {
    Models(ctx context.Context, c Credentials) ([]Model, error)
    Chat(ctx context.Context, req ChatRequest) (ChatStream, error)
}
```

`ChatRequest` carries the model, system prompt, messages (text, tool
calls, tool results), tool definitions as JSON Schema and `MaxTokens`.
`ChatStream` yields text deltas, tool calls, a final `Usage` (input and
output tokens) and a stop reason. History is stored in a provider-neutral
form and converted per call, so a conversation can switch model or
provider mid-way ([22 section 4.2](../reference/22-intelligence.md#42-conversations)).

| `provider` value | Client | Credentials | Model discovery |
|---|---|---|---|
| `Anthropic` | `github.com/anthropics/anthropic-sdk-go`, Messages API with streaming and tools | API key; optional `https` base URL | Models API |
| `OpenAI` | `github.com/openai/openai-go/v3`, Chat Completions with tools | API key; optional `https` base URL (gateways such as LiteLLM) | Models API, filtered to chat models |
| `Google` | `google.golang.org/genai`, Gemini API | API key; optional `https` base URL | Models API, filtered to `generateContent` models |
| `Bedrock` | `github.com/aws/aws-sdk-go-v2/service/bedrockruntime`, `ConverseStream` | An AWS integration's role, assumed by the server with STS through decision 0006's federation, plus a region | `ListInferenceProfiles` and `ListFoundationModels` of `github.com/aws/aws-sdk-go-v2/service/bedrock` |

- Provider strings are the schema's mixed case; other spellings follow
  decision 0022's input strictness until capture C216 says otherwise.
- Validation on save follows
  [22 section 3.3](../reference/22-intelligence.md#33-validation-rules):
  `https` base URLs only, a test completion against each selected model,
  every unreachable model named in the error, and re-validation when the
  key, base URL or model list changes. A Bedrock integration's AWS
  integration must not assume its role on the worker.
- Keys are sealed per decision 0013 in `ai_integrations` and never
  returned. Changing the model list requires the key again, as in
  Spacelift.
- Outbound calls use a 120 s total timeout per model turn and two retries
  on `429` and `5xx` with the provider's `Retry-After`, then fail the turn
  with the provider's message.
- The deprecated `bedrockInferenceProfiles`, `acceptedTermsAndConditionsAI`
  and `latestTermsAndConditionsVersionAI` are not served (0021).

### Data handling

| Rule | Contract |
|---|---|
| What leaves the installation | Only the prompt, history and tool results of a turn, sent to the integration's endpoint. Nothing goes to Spacelift or to the infraweave project; no telemetry |
| Redaction | Before a turn is sent, the masker of decision 0013 replaces every secret value the installation knows (write-only config, sealed columns, sensitive outputs) found in the content; tool results never include write-only fields |
| Logs | Prompts and completions never reach server logs or traces; logs carry integration, model, token counts, latency and tool names only |
| Retention | Conversations are kept until the owner deletes them; `ai.conversations.retention` (default `0`, keep) deletes conversations idle longer than the value |
| Terms | Intelligence is off by default. Enabling it records acceptance of the current terms version (who, when). The terms are the operator's: `ai.terms.version` and `ai.terms.content_file`, defaulting to an infraweave notice that states the data flow above |
| Audit | Integration changes, toggles, Build mode actions and Intent operations are audit events of [0015](0015-events-notifications-webhooks-audit.md), with resource types `AI_INTEGRATION`, `INTENT_PROJECT` and `INTENT_CHAT_CONVERSATION` |

### Account controls and tier values

- The four account toggles of
  [22 section 2.1](../reference/22-intelligence.md#21-settings) are
  served with Spacelift's behaviour: Intelligence, Infra Assistant and
  Build mode default off, enabling requires the terms, and a non-admin can
  request enabling.
- `features.disabled` containing `SPACELIFT_INTELLIGENCE` makes every AI
  integration, Intent, conversation and assistant operation, and the whole
  `/mcp` endpoint, answer `403` `feature-disabled`.
- `ACCOUNT_AI_CONTROL` gates the four toggle mutations; when disabled they
  answer `403` `feature-disabled` and the toggles keep the values in
  `ai.account_defaults`. What Spacelift gates with it is unknown (capture
  C215); this is infraweave's reading.

### One tool layer

`internal/ai/tools` defines every tool once. Infra Assistant, Build mode
and the MCP server select from it, so a capability added to one surface
reaches the others
([22 section 1.1](../reference/22-intelligence.md#11-capability-matrix)).
Every tool runs as the caller, through the same authorization as the REST
API (decision 0007).

| Tool | Surfaces | Contract |
|---|---|---|
| `discover` | MCP, assistant | Browse infraweave's served OpenAPI documents: operations by tag, parameters, request and response schemas |
| `query` | MCP, assistant | Call a `GET` operation by `operationId` with parameters |
| `mutate` | MCP (`mcp:write`) | Call any non-`GET` operation by `operationId` with a body |
| `provider` | MCP, assistant | Provider, resource and data source schemas from the OpenTofu registry, cached |
| `intent` | MCP (`mcp:write`), Build mode | `create`, `update`, `delete`, `refresh`, `import`, `resume`, `read`, `status` and module deployment |
| `navigate`, `page_context` | assistant | UI navigation and the current page's data, supplied by the browser |
| `search_docs` | assistant | Search infraweave's documentation, indexed into the binary at build time |

Destructive tool calls from the assistant (any `delete`, eject, or a
`mutate` on a `DELETE` operation) return a confirmation request to the UI;
the model cannot confirm for the user.

### Infra Assistant and run summaries

- **Chat transport.** `POST /intent-chat-conversations/{id}/messages`
  appends a user message and answers `text/event-stream`: `delta`,
  `tool_call`, `tool_result`, `confirmation_required`, `usage` and `done`
  events. It costs `expensive` (decision 0003). A dropped stream leaves the
  turn running; the stored conversation, versioned per
  [22 section 4.2](../reference/22-intelligence.md#42-conversations), is
  the source of truth.
- **Conversation records** use the reference's operations and the
  `transcript` export unchanged; the owner is the creating user (capture
  C268 asks how Spacelift stores it).
- **Run summaries.** `Summarize` (one phase) and `Explain` (a failed or
  stopped run) open a conversation with a prompt template held in code. The
  assistant reads the run, its history and policy results through `query`;
  each phase log is truncated to its last 64 KiB
  (`ai.summaries.log_bytes`) and masked before it is sent
  ([22 section 4.3](../reference/22-intelligence.md#43-run-summaries)).

### Intent

infraweave implements Intent as
[22 section 5](../reference/22-intelligence.md#5-intent) and
[section 6](../reference/22-intelligence.md#6-intent-policies) describe, on
the tables of [28 section 12.10](../reference/28-data-model.md#1210-intent).

- **Projects.** Space-scoped, with labels, `READ_WRITE` or `READ_ONLY`
  state, configuration elements, a worker pool, a runner image and at most
  one AWS integration attachment with read and write flags and a region.
- **Locks.** The first write of a session takes the project lock; it
  expires after 5 minutes without a write (`intent.lock_timeout`) and each
  write extends it; another session's write answers `409`, problem
  `intent-project-locked`, which decision 0022 lists.
- **Operations.** Each lifecycle call creates an `intent_operations` row
  and evaluates the attached `INTENT` policies through decision 0009:
  allow leads to `PENDING`, no match to `PENDING_REVIEW`, deny to `FAILED`
  with the reasons. A review re-evaluates with `input.reviews`; `ALLOW`
  leads to `READY_FOR_EXECUTION`, `DENY` to `FAILED`; `resume` moves
  `READY_FOR_EXECUTION` to `PENDING`. Captures C220 and C221 may change the
  failed and resume transitions.
- **Execution through workers.** A `PENDING` operation becomes an Intent
  job on the project's worker pool through the run path of decision 0008,
  in the Intent runner image, with the OIDC token and cloud credentials a
  run would get. The job drives the provider plugin directly over the
  Terraform plugin protocol, as `spacelift-intent` does
  ([22 section 8.2](../reference/22-intelligence.md#82-how-it-talks-to-providers-and-clouds)),
  using `github.com/opentofu/provider-client`. Provider packages are
  verified against the registry's `SHA256SUMS` and its signing key before
  use.
- **Asynchrony.** Creates, updates and deletes return an operation ID;
  `status` polls it. Imports wait for completion, up to
  `intent.import_timeout` (default 5 minutes).
- **Dependencies, eject, HCL, modules.** The three dependency types and
  their teardown guard, `intentResourceStateEject`, generated HCL with
  references, `stackCreateFromIntent` (a Repo commit of `main.tofu`, an
  OpenTofu stack, copied configuration and optional state import, then
  `READ_ONLY`), and `spacelift/module` deployments from the registry of
  [0014](0014-module-and-provider-registry.md), all as report 22 records.
- **Access control.** The 28 `INTENT_` and 4 `AI_INTEGRATION_` actions are
  part of decision 0007's catalogue
  ([22 section 5.12](../reference/22-intelligence.md#512-access-control)).

### The MCP server

| Aspect | Contract |
|---|---|
| Endpoint | `/mcp` on the `serve` role, MCP Streamable HTTP, implemented with `github.com/modelcontextprotocol/go-sdk` |
| Tools | `discover`, `query`, `provider`, `mutate`, `intent` from the shared tool layer |
| OAuth | Protected-resource metadata at `/.well-known/oauth-protected-resource/mcp`; authorization code with PKCE and dynamic client registration on decision 0006's authorization server |
| Scopes | `mcp:read` (`discover`, `query`, `provider`) and `mcp:write` (adds `mutate`, `intent`; implies `mcp:read`), as [22 section 7.3](../reference/22-intelligence.md#73-authentication) records |
| Redirect hosts | Loopback, a built-in list of known client hosts, and the account's OAuth redirect allowlist (bare host or one leading `*.`; `https` only) |
| Bearer tokens | An API token of decision 0006 is accepted unnarrowed, as Spacelift accepts `spacectl` tokens |
| `tools` parameter | Narrowing only: intersected with scope, unknown names ignored, `discover` always kept, `intent` brings `provider` ([22 section 7.4](../reference/22-intelligence.md#74-the-tools-query-parameter)) |
| `/intent/mcp` | Not served: merged into `/mcp` and announced for removal, so superseded (0021) |
| Cost class | `tools/call` is `expensive` (decision 0003) |

### Configuration

| Key | Default | Meaning |
|---|---|---|
| `ai.default_integration.provider`, `.credentials_ref`, `.base_url`, `.models` | unset | The installation-provided integration |
| `ai.default_integration.budget.tokens`, `.window` | `0`, `720h` | Its budget; `0` is unlimited |
| `ai.terms.version`, `ai.terms.content_file` | `1`, built-in notice | Terms shown before enabling |
| `ai.account_defaults.*` | all off | Toggle values when `ACCOUNT_AI_CONTROL` is disabled |
| `ai.conversations.retention` | `0` | Idle conversation deletion |
| `ai.summaries.log_bytes` | `65536` | Log tail per phase in summaries |
| `intent.lock_timeout`, `intent.import_timeout` | `5m`, `5m` | Intent timings |

## Rationale

- **P10, hosted only.** The archived server has no policies, locks, RBAC or
  audit, and keeps state in a local file, so mirroring it would add a
  second Intent with weaker guarantees
  ([22 section 8.5](../reference/22-intelligence.md#85-hosted-and-open-source-compared)).
  The stdio proxy keeps local-only MCP clients working without that cost.
- **P11, option (b) with the default off.** It reproduces SaaS behaviour,
  including `isSpaceliftProvided` and `tokenUsage`, without a vendor
  service: the operator supplies the model and decides the budget.
  BYOM-only would leave those fields permanently empty and give an
  operator no way to offer one shared model with a spending cap. Off by
  default matches self-hosted Spacelift and sends nothing anywhere until
  someone configures it.
- **One proxy for every call.** Keys stay server-side, the enabled flag and
  budget are enforced in one place, and redaction cannot be bypassed by a
  surface that forgot it.
- **Official SDKs.** Each provider's own Go SDK tracks API changes; a
  common HTTP layer would have to re-implement four streaming and tool
  formats.
- **Intent on workers.** Running providers in the server, as the archived
  server does, would put cloud credentials and provider binaries in the API
  process. Spacelift runs Intent on worker pools
  ([22 section 5.9](../reference/22-intelligence.md#59-execution)), which
  also lets private pools reach private clouds.
- **Departure from Spacelift:** `discover`, `query` and `mutate` work over
  infraweave's OpenAPI operations rather than GraphQL, because infraweave
  serves no GraphQL (decision 0001).
- **Departure from Spacelift:** the provided integration is the operator's,
  not Spacelift's; `isSpaceliftProvided` means "provided by the
  installation", and its budget applies to every account rather than to a
  Free tier.
- **Departure from Spacelift:** the chat transport is an infraweave-defined
  SSE endpoint, since Spacelift's is unpublished.
- **Departure from Spacelift:** the terms are the operator's, and
  `search_docs` searches infraweave's documentation.
- **Departure from Spacelift:** Intent verifies provider checksums and
  signatures, which `spacelift-intent` does not; what hosted Intent does is
  unknown.

## Constraints

- **Unverified contracts.** The hosted tools' input schemas (C225), the
  OAuth paths (C226), Build mode's access to general mutations (C212), the
  operation transitions (C220, C221) and `ACCOUNT_AI_CONTROL` (C215) are
  infraweave's own until captured
  ([30, Intelligence](../reference/30-open-questions.md#intelligence-report-22)).
- **Intent's release status** is unclear (C214); infraweave ships Intent
  without a preview flag.
- **Provider gaps.** A model a provider's discovery API omits cannot be
  selected; gateways behind a base URL must speak the provider's own API.
- Package names (`internal/ai`, `internal/ai/tools`, `internal/intent`)
  are indicative; decision 0004 owns the layout.

## Verification

- `TestUnitAIProviderConformance`: each provider adapter against a chi
  test server that models its API, covering streaming, tool calls, usage,
  `429` retries and errors.
- `TestUnitAIRedaction`, `TestUnitAIBudget`: masked secrets never reach the
  test server; budget windows, `ai-budget-exhausted` and `Retry-After`.
- `test/acceptance/features/ai_integrations.feature` (`@postgres`, `@ai`):
  create and validate each provider type, sealed keys, the installation
  default, toggles, terms gating, `feature-disabled` and
  `installation-unsupported`.
- `ai_assistant.feature` (`@postgres`, `@ai`): a conversation streamed over
  SSE, a provider switch mid-conversation, confirmation of a destructive
  call, a run summary of a failed run with a masked secret in its log.
- `intent_projects.feature` (`@postgres`, `@tofu`): lock contention,
  policy allow, deny and review paths, an import and a create through a
  worker with a filesystem-mirrored test provider, a tampered provider
  refused, export to a stack.
- `mcp_server.feature` (`@postgres`, `@oidc`): an MCP client from the Go
  SDK registers dynamically, is refused a non-allowlisted redirect, uses
  `mcp:read` and `mcp:write`, narrows with `tools`, and `/intent/mcp`
  answers `404`; `infraweavectl mcp` proxies the same session over stdio.

## References

- [22: Intelligence](../reference/22-intelligence.md), sections
  [3](../reference/22-intelligence.md#3-ai-integrations),
  [5](../reference/22-intelligence.md#5-intent),
  [7](../reference/22-intelligence.md#7-the-spacelift-mcp-server) and
  [8](../reference/22-intelligence.md#8-the-open-source-spacelift-intent).
- [26 section 2.3](../reference/26-scope-and-tiers.md#23-self-hosted) and
  [section 3](../reference/26-scope-and-tiers.md#3-billingtierfeature):
  self-hosted AI limits and the tier values.
- [28 section 12.10](../reference/28-data-model.md#1210-intent): the Intent
  and AI tables.
- [Model Context Protocol](https://modelcontextprotocol.io/specification):
  Streamable HTTP transport and authorization.
- Related records: [0014](0014-module-and-provider-registry.md),
  [0015](0015-events-notifications-webhooks-audit.md),
  [0018](0018-flows-runtime.md),
  [0021](0021-capability-scope-and-supersession.md); decisions 0001, 0003,
  0004, 0006, 0007, 0008, 0009, 0013 and 0022.
