# 22: Intelligence

Checked against: docs.spacelift.io read 2026-10-28 (SaaS and self-hosted `latest`, which is v7.1.0); the policy contract at `app.spacelift.io/.well-known/policy-contract.json` read 2026-10-28; introspection `b3255fe38f5e`; spacelift-api-bruno `7cad4574954b`; terraform-provider-spacelift v1.57.0 (`c72b9bd2f3c3`); spacelift-intent `03a4865d888b`.

Research baseline: **2026-10-28**.

This report records Spacelift Intelligence, the family of AI features in
Spacelift: AI integrations (the LLM connections behind the in-product
assistant), Infra Assistant (the chat panel in the web UI, including run
summaries), Intent (natural-language provisioning inside governed Intent
projects), and the Spacelift MCP server that exposes Intent and the GraphQL API
to external AI clients. It also records the Apache-2.0 `spacelift-intent`
repository, a standalone, local MCP server that shows how Intent drives
Terraform providers without HCL. It is the reference for whoever designs
infraweave's AI integrations, its assistant backend, its Intent engine or its
MCP endpoint. The REST routes for every operation named here are inventoried in
[04](04-platform-api-inventory.md) (sections 10.1 and 10.2); the `INTENT` policy
type's place among the policy types is in [09](09-policies.md); the RBAC model
that the Intent and AI actions plug into is in
[14](14-spaces-rbac-and-identity.md); worker pools and the launcher that run
Intent operations are in [10](10-workers-and-execution.md); AI blocks and the
Flows MCP server belong to report 23.

| Label | Means |
|---|---|
| **[D]** | A docs.spacelift.io page, cited by URL, read 2026-10-28 |
| **[G]** | The introspection snapshot `b3255fe38f5e` |
| **[B]** | spacelift-api-bruno at `7cad4574954b` |
| **[S]** | Source code at the cited commit |
| **[RN]** | The Spacelift changelog, <https://docs.spacelift.io/product/changelog>, or the self-hosted changelog |
| **[I]** | Inferred; the sentence says from what |
| *(unverified)* | No primary source confirms it; listed in 30-open-questions |

## 1. The parts of Spacelift Intelligence

| Part | What it is | Who supplies the model | Source |
|---|---|---|---|
| AI integrations | Stored connections to an LLM provider, used only by Infra Assistant | Spacelift (the Spacelift-provided integration) or the customer (BYOM) | [D] [aii][aii]; [D] [intel][intel] |
| Infra Assistant | A chat panel in the Spacelift UI with an Ask mode and a Build mode; it also delivers run summaries | An AI integration | [D] [ia][ia]; [D] [sum][sum] |
| Intent | Natural-language provisioning and management of cloud resources inside an Intent project, under Intent policies, managed state and an audit trail | The customer's own AI client (MCP) or Infra Assistant's model (Build mode) | [D] [intent][intent] |
| Spacelift MCP server | A remote MCP server at `/mcp` that exposes the GraphQL API and the Intent tools to external AI clients | The customer's AI client | [D] [mcp][mcp] |
| Intent policy | The `INTENT` policy type, evaluated on every Intent resource operation from either surface | n/a | [D] [ip][ip]; [G] `PolicyType.INTENT` |
| `spacelift-intent` (open source) | A local stdio MCP server that calls provider plugins directly and keeps state in SQLite; no Spacelift account involved | The customer's AI client | [S] [si-readme][si-readme] |

The two surfaces share one toolset. The MCP server is reached from an editor or
terminal; Infra Assistant wraps the same capabilities in the UI and adds
navigation, page context, documentation search and run explanation [D]
[intel][intel]. Either surface works on one shared set of Intent projects,
governed by one set of Intent policies [D] [intel][intel].

General API queries and mutations, UI navigation and documentation search work
account-wide without a project; provisioning always happens inside an Intent
project [D] [intel][intel].

### 1.1 Capability matrix

| Capability | MCP server | Infra Assistant | Source |
|---|---|---|---|
| Read the account through GraphQL | yes (`query`) | yes (Ask) | [D] [intel][intel] |
| Discover provider schemas | yes (`provider`) | yes | [D] [intel][intel] |
| Create, update, delete and import resources | yes, with `mcp:write` | yes, in Build mode | [D] [intel][intel] |
| Run arbitrary GraphQL mutations | yes (`mutate`, with `mcp:write`) | *(unverified)*: the Build mode tool list names resource, state and dependency operations, not general mutations | [D] [mcp][mcp]; [D] [ia][ia] |
| Governed by Intent policy | yes | yes, in Build mode | [D] [intel][intel] |
| Navigate the UI, read page context | no | yes | [D] [ia][ia] |
| Search the Spacelift documentation | no | yes | [D] [ia][ia] |
| Summarize and explain runs | no | yes | [D] [sum][sum] |

## 2. Account settings and enablement

### 2.1 Settings

The settings live under **Settings** > **Spacelift Intelligence** [D]
[intel][intel].

| Setting (UI) | GraphQL read | GraphQL write | Behaviour | Source |
|---|---|---|---|---|
| Accept the Spacelift Intelligence Terms of Use | `spaceliftIntelligenceTermsAccepted: Boolean!`; `spaceliftIntelligenceTermsLatestVersion: TermsAndConditionsVersion` (`content: String!`, `version: Int!`) | implied by `accountSetSpaceliftIntelligenceEnabled(enabled: true)` | Asked the first time any Intelligence feature is enabled | [G]; [D] [intel][intel] |
| **Enable Spacelift Intelligence AI features** | `spaceliftIntelligenceEnabled: Boolean!`, true when any sub-feature is on | `accountSetSpaceliftIntelligenceEnabled(enabled: Boolean!): Boolean!` | Enabling accepts the terms and turns on every sub-feature | [G] |
| **Enable Infra Assistant** | `infraAssistantEnabled: Boolean!` | `accountSetInfraAssistantEnabled(enabled: Boolean!): Boolean!` | Requires the terms to be accepted first | [G]; [D] [ia][ia] |
| **Ask-only mode** or **Ask and build mode** | `infraAssistantBuildModeEnabled: Boolean!` | `accountSetInfraAssistantBuildModeEnabled(enabled: Boolean!): Boolean!` | Build mode is off by default; requires the terms first | [G]; [D] [ia][ia]; [RN] [sh-cl][sh-cl] v6.0.0 |
| Request enabling | n/a | `accountRequestInfraAssistantEnabling: Boolean!` | A non-admin asks an admin to enable Infra Assistant | [G] |

- Spacelift Intelligence is off by default, opt-in by an organization admin,
  and optional for using the rest of the platform [D] [ai-terms][ai-terms].
- Intent through the MCP server needs none of these toggles; it is always
  available once a client authenticates [D] [intel][intel].
- Two deprecated queries remain in the schema:
  - `acceptedTermsAndConditionsAI`, replaced by
    `spaceliftIntelligenceTermsAccepted` [G];
  - `latestTermsAndConditionsVersionAI`, replaced by
    `spaceliftIntelligenceTermsLatestVersion` [G].

  Both were deprecated on 2026-08-27 [RN] [cl][cl].
- The `llmVendor` query and the `changeLLMVendor` mutation, an earlier
  account-level vendor choice, were deprecated on 2026-07-16 and removed on
  2026-08-21 [RN] [cl][cl]; they are absent from the snapshot [G].
- Bruno files the six account toggles and the two terms queries under
  "Account-wide administrative setting" [B].

### 2.2 Tier and release status

| Item | Status | Source |
|---|---|---|
| Billing feature flags | `BillingTierFeature` has `SPACELIFT_INTELLIGENCE` and `ACCOUNT_AI_CONTROL` | [G] |
| Infra Assistant | Available on every plan | [D] [ia][ia] |
| Intent through MCP | Always on; tokens are paid by the customer's AI client | [D] [intel][intel] |
| Token budget, Spacelift-provided integration | Free tier: USD 10 per 30-day window, reset in full; paid tiers: unlimited | [D] [intel][intel] |
| BYOM integrations | No Spacelift-imposed limit; billed by the provider | [D] [aii][aii] |
| Price of Intelligence | Free of charge for now; Spacelift reserves the right to charge with notice | [D] [ai-terms][ai-terms] §4 |
| Spacelift-hosted AI usage | The same terms say Spacelift-hosted AI consumes Spacelift credits and is subject to usage limits Spacelift communicates; how credits relate to the free tier's USD 10 budget is not stated *(unverified)* | [D] [ai-terms][ai-terms] §3 |
| Intent release status | The legal page is titled "Spacelift Intent Early Access Release", effective 2025-10-08, and treats Intent as a beta, not recommended for production | [D] [intent-terms][intent-terms] |
| Intent in the UI | Reached under _Try New Features_ > _Intent Projects_ | [D] [i-wp][i-wp] |
| Intent and chat in Bruno | 36 requests carry the marker "Intent and AI chat preview feature" | [B] |
| MCP `tools` parameter | Alpha: may break, best-effort support | [D] [mcp-c][mcp-c]; [D] [support][support] |
| Self-hosted | Intent, the MCP server, Infra Assistant and Intent policies ship from v6.0.0; run summaries from v7.0.0; no Spacelift-provided integration, so BYOM is required and no token budget applies | [RN] [sh-cl][sh-cl]; [D] [sh-intel][sh-intel]; [D] [sh-aii][sh-aii] |

Whether Intent has left early access is not stated: the product pages carry
no beta label, while the legal page and the UI menu do. *(unverified)*

Which tier `ACCOUNT_AI_CONTROL` gates, and what it controls, is not documented.
*(unverified)*

## 3. AI integrations

### 3.1 The entity

`AIIntegration` has 16 fields [G].

| Field | Type | Meaning | Source |
|---|---|---|---|
| `id` | `ID!` | Integration ID | [G] |
| `space` | `Space` | Owning space; null for the Spacelift-provided integration | [G] |
| `name` | `String!` | Display name | [G] |
| `description` | `String` | Optional | [G] |
| `labels` | `[String!]!` | User labels | [G] |
| `provider` | `String!` | One of Anthropic, OpenAI, Google, Bedrock | [G] |
| `providerIntegrationId` | `ID` | The AWS integration behind a Bedrock integration | [G] |
| `providerIntegrationName` | `String` | Its display name | [G] |
| `providerConfig` | `AIProviderConfig` | Union; its only member is `AWSBedrockConfig` (`region: String!`, `profiles: [String!]!`) | [G] |
| `baseURL` | `String` | Custom gateway URL; empty means the provider default; not used for Bedrock | [G] |
| `models` | `[String!]` | Pinned models; null means the provider's default list; never null for Bedrock, where it holds the stored inference profile IDs | [G] |
| `modelDetails` | `[AIModel!]!` | `id` (canonical model ID such as `claude-sonnet-4-6`, or a Bedrock profile ID) and display `name`; resolved live from AWS for Bedrock and may return fewer entries | [G] |
| `tokenUsage` | `AIIntegrationTokenUsage!` | Account-level budget window: `totalTokens: Int!`, `usedTokens: Int!`, `tokensResetAt: Int` (Unix time) | [G] |
| `isSpaceliftProvided` | `Boolean!` | Read-only integration managed by Spacelift and visible to all accounts | [G] |
| `enabled` | `Boolean!` | Disabled integrations stay visible and editable but the AI provider proxy rejects calls to them; Spacelift-provided ones read as disabled while Intelligence is off, and turning Intelligence on restores the last choice | [G] |
| `allowedActions` | `[Action!]!` | AI integration actions the caller may perform | [G] |

The `enabled` field's description names an "AI provider proxy" that rejects
calls through disabled integrations [G]. This implies a server-side proxy
through which every LLM call passes, so Spacelift holds the keys and the
browser never talks to a provider directly [I] from the `enabled` description
and from the keys never being displayed [D] [aii][aii].

Provider strings: the schema description lists Anthropic, OpenAI, Google and
Bedrock [G]; the Terraform provider sends `Anthropic`, `Bedrock`, `Google` and
`OpenAI` [S] [tf-ai][tf-ai]; Bruno's example sends `OPENAI` [B]. The schema
and the provider agree, so mixed case is the documented value; whether the API
also accepts upper case is *(unverified)*.

### 3.2 Providers and models

| Mode | Provider | Credentials | Models | Base URL | Source |
|---|---|---|---|---|---|
| Spacelift-provided (_Spacelift default_) | AWS Bedrock, serving Anthropic models | None; managed by Spacelift | Claude Sonnet 4.6 and Claude Opus 4.8 | n/a | [D] [aii][aii] |
| BYOM | Anthropic | API key | Discovered live from the provider API | Optional, `https` only | [D] [aii][aii] |
| BYOM | OpenAI | API key | Discovered live | Optional, `https` only | [D] [aii][aii] |
| BYOM | Google (Gemini API) | API key | Discovered live | Optional, `https` only | [D] [aii][aii]; [S] [tf-ai][tf-ai] |
| BYOM | AWS Bedrock | An existing AWS integration plus a region; Spacelift assumes the integration's role from its own side | The inference profiles chosen at setup | Not used | [D] [aii][aii]; [G] |

- Discovery filters out well-known non-chat models, such as speech and image
  models [D] [aii][aii].
- A custom base URL lets the integration route through a provider-compatible
  gateway such as LiteLLM [D] [aii][aii].
- Bedrock was added as a BYOM provider on 2026-04-13 [RN] [cl][cl].
- The Spacelift-hosted model providers listed in the AI terms are AWS Bedrock,
  Anthropic and the Google Gemini API, and Spacelift may change them without
  notice [D] [ai-terms][ai-terms] §3.

### 3.3 Validation rules

| Rule | Source |
|---|---|
| A base URL must be `https` | [D] [aii][aii] |
| At save, a gateway is checked by confirming it accepts the key and by running a test completion against each selected model; any unreachable model fails the save and is named in the error | [D] [aii][aii] |
| Changing the key, base URL or model list re-runs that validation | [D] [aii][aii] |
| Changing the model selection needs the key re-entered, so the live list can be fetched; the stored key is never shown | [D] [aii][aii] |
| A Bedrock integration's AWS integration must not have **Assume role on worker** enabled | [D] [aii][aii] |
| Bedrock profiles are validated against AWS at save | [D] [aii][aii]; [S] [tf-ai][tf-ai] |
| The Bedrock role needs `bedrock:InvokeModel` and `bedrock:InvokeModelWithResponseStream` on both `foundation-model/*` (any region) and the account's `inference-profile/*` ARNs, plus `bedrock:ListInferenceProfiles` and `bedrock:ListFoundationModels` on `*` | [D] [aii][aii] |
| Listing and validation work with read-only access, so a missing invoke permission shows only when a chat fails | [D] [aii][aii] |
| The Spacelift-provided integration cannot be created, updated or deleted, only toggled | [G] `aiIntegrationUpdate`; [D] [rbac][rbac] |
| Enabling the Spacelift-provided integration requires Intelligence to be on; disabling is always allowed | [G] `aiIntegrationToggle` |

### 3.4 Operations

| Operation | Kind | Arguments and result | Source |
|---|---|---|---|
| `aiIntegrations` | query | `[AIIntegration!]!` | [G] |
| `aiIntegration(id)` | query | `AIIntegration` | [G] |
| `availableAIProviders(provider: String)` | query | `[AIProvider!]!`, each with `provider`, `models` and `modelDetails` | [G] |
| `availableAIModels(provider!, apiKey, providerIntegrationId, providerConfig, baseURL, space)` | query | Live models for an unsaved configuration; API-key providers need `apiKey` (and `baseURL` for a gateway); Bedrock needs `providerIntegrationId` and `providerConfig.bedrock.region`; requires admin on `space` | [G] |
| `bedrockInferenceProfiles(awsIntegration!, config!)` | query, deprecated 2026-08-27 | Replaced by `availableAIModels` | [G]; [RN] [cl][cl] |
| `aiIntegrationCreate(name!, description, labels, provider!, apiKey, providerIntegrationId, providerConfig, baseURL, models, space)` | mutation | `AIIntegration!` | [G] |
| `aiIntegrationUpdate(id!, name!, provider!, ...)` | mutation | Same arguments as create plus `id` | [G] |
| `aiIntegrationToggle(id!, enabled!)` | mutation | `AIIntegration!` | [G] |
| `aiIntegrationDelete(id!)` | mutation | `AIIntegration` | [G] |

`AIProviderConfigInput` takes exactly one provider field; the only one is
`bedrock: AWSBedrockConfigInput` (`region: String!`, `profiles: [String!]!`)
[G]. `IntegrationsCount.ai` counts AI integrations [G]. The audit trail has the
resource type `AI_INTEGRATION` with metadata `id` and `name` [G].

### 3.5 Access control

| Action (UI) | `Action` value | Scope | Legacy fallback | Source |
|---|---|---|---|---|
| Create AI integration | `AI_INTEGRATION_CREATE` | The integration's space | Admin | [D] [rbac][rbac]; [G] |
| Update AI integration | `AI_INTEGRATION_UPDATE` | The integration's space | Admin | [D] [rbac][rbac]; [G] |
| Delete AI integration | `AI_INTEGRATION_DELETE` | The integration's space | Admin | [D] [rbac][rbac]; [G] |
| Toggle AI integration | `AI_INTEGRATION_TOGGLE` | The integration's space; the root space for the Spacelift-provided one | Admin | [D] [rbac][rbac]; [G] |

Users see and select only the integrations in spaces they can access [D]
[ia][ia]. Several integrations may exist per account, for different
providers, teams or environments [D] [aii][aii].

### 3.6 Terraform resource

`spacelift_ai_integration` takes exactly one provider block [S]
[tf-ai][tf-ai]; changing which block is set replaces the resource, because
the API does not let an integration change provider (resource description in
`terraform-schema.json`) [S]. Report [05](05-terraform-provider.md)
owns the provider; the attributes are:

| Block or attribute | Type | Notes |
|---|---|---|
| `name` | string | Required except on a Spacelift-provided integration |
| `description`, `labels`, `space_id` | string, set, string | Optional |
| `enabled` | bool | Default `true` |
| `models` | list of string | Empty means follow the provider default; not for Bedrock |
| `ai_provider`, `is_spacelift_provided`, `id` | computed | |
| `anthropic`, `openai`, `google` | block, max 1 | `api_key` (deprecated, sensitive), `api_key_wo` and `api_key_wo_version` (write-only, Terraform/OpenTofu 1.11+), `base_url` |
| `bedrock` | block, max 1 | `integration_id`, `region`, `profiles` (required), `integration_name` (computed) |
| `spacelift` | block, max 1, no arguments | Marks the Spacelift-provided integration, which can only be imported and toggled |

Data sources `spacelift_ai_integration` and `spacelift_ai_integrations` exist
[S] [tf-ai][tf-ai] (provider schema).

### 3.7 Data handling

| Rule | Source |
|---|---|
| API keys are encrypted at rest and never displayed after save | [D] [aii][aii] |
| The provider's Terraform state stores an empty string for a deprecated `api_key` and nothing for `api_key_wo` | [S] [tf-ai][tf-ai] (schema descriptions) |
| Customer data is processed only to deliver the AI features | [D] [ai-terms][ai-terms] §5 |
| On the Spacelift-provided integration, Spacelift does not train on customer data and forbids its model providers to | [D] [ai-terms][ai-terms] §5 |
| On BYOM, the chosen provider's terms govern processing and Spacelift disclaims it | [D] [ai-terms][ai-terms] §3, §5 |
| Customers must not send PII, PHI, biometric or card data, or credentials, through the AI features | [D] [ai-terms][ai-terms] §6 |
| Outputs are provided as is; human review is the customer's responsibility | [D] [ai-terms][ai-terms] §8 |
| The Intent early-access terms add: no backups, data may be deleted or reset, and usage analytics are collected | [D] [intent-terms][intent-terms] §5 |
| Self-hosted Flows must use BYOM for AI blocks (report 23) | [D] [ai-terms][ai-terms] §3 |

## 4. Infra Assistant

### 4.1 Modes and tools

| Mode | Tools | Source |
|---|---|---|
| Ask (default) | **Navigate** (go to pages), **Page context** (read the current page), **Search docs**, **Inspect GraphQL** (schema), **Query GraphQL** (read-only), **Inspect provider schemas** | [D] [ia][ia] |
| Build | Ask's tools plus resource operations (create, update, delete), state management and dependency management, from the same MCP toolset as Intent; module deployment as well | [D] [ia][ia]; [RN] [sh-cl][sh-cl] v6.2.0 |

- Build mode must be enabled by an admin; when it is off, the mode switcher
  shows Build as disabled and points the user to an admin [D] [ia][ia].
- Every Build mode operation passes Intent policy evaluation [D] [ia][ia].
- The assistant asks for confirmation before destructive operations [D]
  [ia][ia].
- Every Build mode action is written to the audit trail [D] [ia][ia].

### 4.2 Conversations

| Item | Detail | Source |
|---|---|---|
| Entity | `IntentChatConversation`: `id`, `description`, `createdAt`, `updatedAt`, `version` (monotonic, bumped on message changes), `lastUsedAIIntegration`, `lastUsedAIModel`, `messages`, `transcript(offset, limit)` | [G] |
| Message | `IntentChatMessage`: `id`, `role` (user, assistant, system and others), `content` (JSON) | [G] |
| Transcript | Markdown of the conversation with tool calls and results verbatim and reasoning dropped, pageable by message offset and limit, meant for continuing in an external agent harness | [G] |
| Operations | `intentChatConversations(page, perPage, search)`, `intentChatConversation(id)`, `intentChatConversationCreate`, `intentChatConversationUpdate(id, description!)`, `intentChatConversationDelete(id)` | [G] |
| UI | History is kept per user; conversations can be browsed, searched, switched and deleted | [D] [ia][ia] |
| Model switching | The integration and model can change mid-conversation; history is adapted to the new provider's format | [D] [aii][aii] |
| Audit | Resource type `INTENT_CHAT_CONVERSATION` with metadata `id` and `name` | [G] |
| Panel | A resizable left sidebar on desktop, a floating window on small screens; a dedicated page and an **Ask** button by the search box | [D] [ia][ia]; [RN] [sh-cl][sh-cl] v6.4.1 |

No GraphQL operation sends a chat message or streams a reply; the snapshot
has only conversation CRUD and the transcript [G]. The chat exchange therefore
uses another channel *(unverified)*, which also carries the token-budget
accounting.

### 4.3 Run summaries

| Item | Detail | Source |
|---|---|---|
| Entry points | **Summarize** on one completed phase; **Explain** on a whole failed or stopped run | [D] [sum][sum] |
| Mechanism | Opens Infra Assistant with a prepared prompt; the assistant reads the run's state, its history and the policy results before answering | [D] [sum][sum]; [RN] [cl][cl] 2026-06-23 |
| Output | A plain-language summary, the cause and suggested fixes, including code snippets; wording varies | [D] [sum][sum] |
| Model | Whatever the Infra Assistant model picker has selected | [D] [sum][sum] |
| Who may use it | Every user with read access to runs, once Infra Assistant is enabled | [D] [sum][sum] |
| Coverage | All plans and all stack types since 2026-06-23; self-hosted from v7.0.0 | [RN] [cl][cl]; [RN] [sh-cl][sh-cl] |

The self-hosted v6.0.0 notes call the earlier summarization feature "Saturnhead
Assist" [RN] [sh-cl][sh-cl].

## 5. Intent

### 5.1 Concepts

| Term | Meaning | Source |
|---|---|---|
| Intent project | The governed container for Intent work: resources, their state, operation history, attached policies, cloud integrations, environment, worker pool and runner image | [D] [intel][intel]; [G] `IntentProject` |
| Intent resource | One managed cloud resource (or a module deployment) in a project's state | [G] `IntentResource` |
| Operation | One lifecycle action on a resource, recorded with policy results and reviews | [G] `IntentResourceOperation` |
| Dependency | A recorded link from a resource that uses a value to the resource that produced it | [D] [i-wp][i-wp]; [G] |
| Active project | The project the caller's MCP or UI commands target; per caller | [G] `IntentProject.isActive` |
| Project lock | Exclusive write access for one session | [D] [i-gs][i-gs] |

The pipeline: the client sends a prompt; Intent discovers providers in the
OpenTofu registry and reads their schemas; attached policies evaluate the
proposed operation; approved operations run against the cloud with scoped
credentials; state and history are recorded [D] [intent][intent].

### 5.2 `IntentProject`

24 fields [G].

| Field | Type | Meaning |
|---|---|---|
| `id` | `ID!` | Unique, immutable slug within the organization |
| `name` | `String!` | Mutable display name |
| `description` | `String` | Markdown |
| `labels` | `[String!]!` | Used for policy auto-attachment and filtering |
| `space` | `Space!` | Owning space |
| `createdAt`, `updatedAt` | `Int!` | Unix times |
| `lastResourcesUpdatedAt` | `Int` | Last change to any resource |
| `createdBy` | `String` | Creator |
| `state` | `String!` | Project-wide mode: `READ_WRITE` or `READ_ONLY` [D] [i-iac][i-iac] |
| `isActive` | `Boolean!` | Whether this is the caller's most recently activated project; always false for machine sessions |
| `lockedBy`, `isLocked`, `lockedUntil` | `String`, `Boolean!`, `Int` | Lock holder, flag and expiry |
| `workerPool` | `WorkerPool` | Assigned pool; null means the account default |
| `runnerImage` | `String!` | Empty means the system default Intent worker image |
| `awsIntegrationIntentProjectAttachment` | `AwsIntegrationIntentProjectAttachment` | The attached AWS integration (a single value) |
| `attachedPolicies` | `[IntentProjectPolicyAttachment!]!` | Attached policies |
| `attachablePolicies` | `[Policy!]!` | Policies that may be attached |
| `configElements` | `[ConfigElement!]!` | Environment variables and file mounts |
| `hcl(page, pageSize)` | `IntentProjectHclResult!` | Generated HCL; `page` from 1, `pageSize` 1 to 500, default 50; returns `hcl` and `hasMore` |
| `searchIntentResources`, `searchIntentResourcesOperations` and their `...Suggestions` | search outputs | Search within the project |
| `allowedActions` | `[Action!]!` | Intent actions the caller may perform |

### 5.3 Project operations

| Operation | Arguments | Effect | Source |
|---|---|---|---|
| `intentProjects` | none | All projects | [G] |
| `intentProject` | `id` | One project | [G] |
| `intentProjectActive` | none | The caller's active project | [G] |
| `searchIntentProjects`, `searchIntentProjectsSuggestions` | `SearchInput`, `SearchSuggestionsInput` | Search across projects | [G] |
| `intentProjectCreate` | `name!`, `description`, `labels`, `space!`, `workerPool`, `runnerImage` | Create | [G] |
| `intentProjectUpdate` | `id!` and optional `name`, `description`, `labels`, `space`, `workerPool`, `runnerImage` | Update | [G] |
| `intentProjectDelete` | `id!`, `keepResources` | Delete; `keepResources` presumably drops the project without destroying cloud resources *(unverified)* | [G] |
| `intentProjectActivate` | `id!` | Make it the caller's active project | [G]; [D] [i-gs][i-gs] |
| `intentProjectUnlock` | `id!` | Release the lock | [G] |
| `intentProjectDisable` | `id!` | Switch to `READ_ONLY` *(unverified)*: inferred from `intentProjectEnable` restoring `READ_WRITE` | [G]; [D] [i-iac][i-iac] |
| `intentProjectEnable` | `id!` | Switch back to `READ_WRITE` | [G]; [D] [i-iac][i-iac] |
| `intentProjectAttachWorkerPool` | `intentProject!`, `workerPool!` | Assign a private pool | [G]; [D] [i-exec][i-exec] |
| `intentProjectDetachWorkerPool` | `intentProject!` | Return to the default pool | [G] |
| `intentProjectConfigAdd`, `intentProjectConfigUpdate` | `project!`, `config: ConfigInput!` (`id`, `type`, `value`, `description`, `writeOnly`, `fileMode`) | Add or change an environment variable or file mount | [G] |
| `intentProjectConfigDelete` | `project!`, `config!` | Remove one | [G] |
| `awsIntegrationIntentProjectAttach` | `intentProject!`, `integration!`, `read!`, `write!`, `awsRegion!` | Attach AWS with read and write flags and a region | [G]; [D] [i-wp][i-wp] |
| `awsIntegrationIntentProjectDetach` | `intentProject!`, `integration!` | Detach | [G] |
| `policyIntentProjectAttach` | `id!` (policy), `intentProject!` | Attach an `INTENT` policy | [G] |
| `policyIntentProjectDetach` | `id!` (attachment) | Detach | [G] |
| `stackCreateFromIntent` | `project!`, `importState`, `stackName` | Export to a stack (section 5.10) | [G] |

There is no lock mutation: the lock is taken implicitly when a session first
changes the project [D] [i-gs][i-gs], although the RBAC catalogue has a
**Lock intent project** action [D] [rbac][rbac].

`StackCreateFromIntentInput` (`project: ID!`, `stackName: String`) is in the
schema but `stackCreateFromIntent` takes plain arguments [G].

Project audit events use resource type `INTENT_PROJECT` with metadata `id` and
`name` [G].

### 5.4 Locks and sessions

- **One writer.** A project accepts work from a single session at once [D]
  [i-wp][i-wp].
- **Taking the lock.** The first change in a session locks the project to that
  user [D] [i-gs][i-gs].
- **Expiry.** The lock expires after about five minutes of inactivity, and each
  change extends it [D] [i-gs][i-gs].
- **Release.** The lock can also be released explicitly in the UI or the API
  (`intentProjectUnlock`) [D] [i-ts][i-ts]; [G].
- **Conflicts.** A write from another session fails while the lock is held [D]
  [i-ts][i-ts].
- **Active project.** Being active is separate from the project's state:
  `isActive` is per caller, while `state` is shared by everyone [G].

### 5.5 Resources, operations and reviews

`IntentResource` [G]:

| Field | Type | Meaning |
|---|---|---|
| `resourceID` | `String!` | ID in state |
| `intentProjectID` | `String!` | Owning project |
| `resourceType` | `String!` | Provider type, such as `aws_s3_bucket`, or `spacelift/module` |
| `address` | `String!` | Address in the module |
| `name` | `String!` | Name |
| `provider` | `String!` | Provider name |
| `operation` | `String!` | Last operation |
| `changedBy` | `String!` | Who changed it |
| `state` | `String!` | Resource state as JSON |
| `hcl` | `String!` | Generated HCL for this resource |
| `dependencies(direction)` | `[IntentResourceDependency!]!` | Edges, filtered by `IntentResourceDependencyDirection` |

`IntentResourceOperation` [G]:

| Field | Type | Meaning |
|---|---|---|
| `id` | `ID!` | Operation ID |
| `resourceID`, `resourceType`, `provider`, `operation` | `String!` | Subject and kind |
| `allow`, `deny` | `[String!]!` | Policy reasons collected |
| `state` | `IntentResourceOperationState!` | See below |
| `failed` | `String` | Failure reason |
| `isExistingResource` | `Boolean!` | Whether it targets an existing resource |
| `reviews` | `[IntentResourceOperationReview!]!` | `id`, `author`, `decision`, `note`, `timestamp` |
| `currentState`, `proposedState` | `String` | Before and after, as JSON |
| `createdAt` | `Int!` | Time |
| `author` | `String` | Who requested it |

`IntentResourceOperationState` [G]:

| Value | Meaning |
|---|---|
| `UNKNOWN` | Invalid zero value |
| `PENDING_REVIEW` | Waiting for a manual allow or deny |
| `READY_FOR_EXECUTION` | Reviewed and ready to run |
| `PENDING` | Waiting to execute |
| `FINISHED` | Succeeded |
| `FAILED` | Failed |

`IntentResourceOperationReviewDecision` is `ALLOW` or `DENY`; a review is
recorded with `intentResourceOperationReview(intentProject!, operationID!,
decision!, note)` [G].

The transitions are not documented. From the policy outcomes and the enum
descriptions [I]:

| From | Event | To |
|---|---|---|
| (new) | policies allow | `PENDING` |
| (new) | no rule matches | `PENDING_REVIEW` |
| (new) | a policy denies | `FAILED` *(unverified)*, with `deny` reasons set |
| `PENDING_REVIEW` | review `ALLOW` | `READY_FOR_EXECUTION` |
| `PENDING_REVIEW` | review `DENY` | `FAILED` *(unverified)* |
| `READY_FOR_EXECUTION` | the client calls the `intent` tool's `resume` | `PENDING` *(unverified)* |
| `PENDING` | the worker applies | `FINISHED` or `FAILED` |

Policy evaluation reruns on review with the recorded reviews in
`input.reviews` (section 6), which suggests the transition out of
`PENDING_REVIEW` re-evaluates rather than trusting the review alone [I] from
the contract.

Search: `searchIntentResources`, `searchIntentResourcesOperations` and their
suggestion queries exist at the root and on `IntentProject` [G]. The
searchable fields are returned by the suggestion queries at run time, not
fixed in the schema [G].

### 5.6 Lifecycle operations from the client

Through the MCP `intent` tool, an Intent client can `create`, `update`,
`delete`, `refresh`, `import`, `resume`, `read` and `status`, and deploy
registry modules [D] [intel][intel]; [D] [mcp][mcp].

- **Asynchrony.** Slow creates, updates and deletes run asynchronously: the
  tool returns an operation ID [D] [i-hc][i-hc].
- **Polling.** The client polls `status` with exponential backoff [D]
  [i-hc][i-hc].
- **Re-issued prompts.** Sending the same prompt again normally launches a
  fresh operation rather than picking up the running one [D] [i-hc][i-hc].
- **Imports** always run synchronously [D] [i-hc][i-hc].
- **Discovery.** Existing cloud resources can be listed through data-source
  reads and imported into the project's state [D] [i-wp][i-wp].
- **Eject.** `intentResourceStateEject(intentProject!, resourceID!)` removes a
  resource from state without destroying it [G]; [D] [rbac][rbac].
- **Read.** **Read intent state** also covers data source reads [D]
  [rbac][rbac].

### 5.7 Dependencies

| Item | Detail | Source |
|---|---|---|
| Entity | `IntentResourceDependency`: `id`, `fromResourceID` (the dependent), `toResourceID` (the dependency), `dependencyType`, `explanation`, `fieldMappings` (`sourceField`, `targetField`, `description`) | [G] |
| Types | `IntentResourceDependencyType`: `EXPLICIT`, `IMPLICIT`, `DATA_SOURCE` | [G] |
| Directions | `IntentResourceDependencyDirection`: `DEPENDENCIES`, `DEPENDENTS`, `BOTH` | [G] |
| Mutations | `intentResourceDependencyAdd(intentProject!, fromResourceID!, toResourceID!, dependencyType!, explanation, fieldMappings)`, `intentResourceDependencyRemove(intentProject!, fromResourceID!, toResourceID!)` | [G] |
| Who records them | The assistant, as it works; users do not manage them by hand | [D] [i-wp][i-wp] |
| Teardown guard | A resource with dependents cannot be deleted; the dependents go first | [D] [i-wp][i-wp] |
| Ordering | Not enforced; the assistant orders creates and reverses for teardown | [D] [i-wp][i-wp] |
| Export | Dependencies become references in generated HCL, such as `aws_subnet.main.id` or `${module.lb.dns_name}` | [D] [i-wp][i-wp]; [D] [i-iac][i-iac] |

Deletion follows: check dependents, ask for confirmation (default), then apply
or stop on a policy denial; history records attempted and successful deletes
[D] [i-wp][i-wp].

### 5.8 Cloud credentials and configuration

| Cloud | Mechanism | Source |
|---|---|---|
| AWS | An AWS integration attached to the project with a region and separate read and write flags | [D] [i-wp][i-wp]; [G] |
| Azure | Service-principal environment variables on the project: `ARM_CLIENT_ID`, `ARM_CLIENT_SECRET`, `ARM_TENANT_ID`, `ARM_SUBSCRIPTION_ID` | [D] [i-cred][i-cred] |
| GCP | Environment variable `GOOGLE_APPLICATION_CREDENTIALS` pointing to a mounted `gcp.json` (for example under `/mnt/workspace/`), optional `GOOGLE_PROJECT`, and a `spacelift.oidc` file that Spacelift mounts | [D] [i-cred][i-cred] |

The UI calls AWS the only cloud integration supported today; Azure and GCP go
through project configuration [D] [i-wp][i-wp]. Project configuration uses
the same `ConfigElement` model as stacks (report 13) [G]. The automatic
`spacelift.oidc` mount implies Intent executions receive the same OIDC token
as runs (report 12) [I] from [i-cred][i-cred].

### 5.9 Execution

| Item | Detail | Source |
|---|---|---|
| Where | The account's default worker pool (the public pool on SaaS) unless a private pool is attached | [D] [i-exec][i-exec] |
| Image | The system default Intent worker image unless `runnerImage` is set; the launcher picks up the image for each execution | [D] [i-exec][i-exec]; [G] |
| Pool visibility | Pools visible to a project follow its space inheritance | [D] [i-exec][i-exec] |
| Pool relations | `WorkerPool.intentProjects`, `intentProjectsCount`, `searchIntentProjects`; the same on `PublicWorkerPool` for the account | [G] |
| Release dates | Private pools on 2026-04-13; per-project runner image on 2026-05-14 | [RN] [cl][cl] |

Intent operations therefore travel the same launcher and worker path as runs
(report 10), with an Intent-specific image [I] from [i-exec][i-exec]. The
default image name, its contents and the message the launcher receives for an
Intent execution are not documented *(unverified)*.

### 5.10 Intent to IaC

| Step | Detail | Source |
|---|---|---|
| Per-resource HCL | Shown in the resource drawer; `IntentResource.hcl` | [D] [i-iac][i-iac]; [G] |
| Project HCL | **Download HCL file(s)**; contains a `terraform` block with required providers and versions, every resource, and references between them; `IntentProject.hcl` pages it | [D] [i-iac][i-iac]; [G] |
| Stack creation | `stackCreateFromIntent(project, importState, stackName)`; the UI drawer has **Stack name** (defaults to the project name) and **Transfer state** (default on) | [G]; [D] [i-iac][i-iac] |
| What it does | Writes `main.tofu` to a new Spacelift Repo, creates a stack on OpenTofu linked to it, copies the project's environment variables and cloud integrations, and with state import uploads state so the first tracked run shows no changes | [D] [i-iac][i-iac] |
| After import | The project becomes `READ_ONLY`: MCP lifecycle writes (`create`, `update`, `delete`, `refresh`, `import`, `resume`) and GraphQL dependency edits and state eject are rejected; viewing still works | [D] [i-iac][i-iac]; [RN] [cl][cl] 2026-05-14 |
| Without import | The project stays writable | [D] [i-iac][i-iac] |
| Recovery | `intentProjectEnable` or the UI **Enable** action restores `READ_WRITE` | [D] [i-iac][i-iac] |

Spacelift Repos are covered with stacks in report 07.

### 5.11 Spacelift registry modules

| Item | Detail | Source |
|---|---|---|
| Resource type | `spacelift/module`; one resource per deployment, with the module's resources inside its state | [D] [i-mod][i-mod] |
| Source | Only the Spacelift module registry; not the public registry or Git | [D] [i-mod][i-mod] |
| Identity | Module slug `terraform-<provider>-<name>` and an exact version; the assistant resolves both and picks the latest active version | [D] [i-mod][i-mod] |
| Checks before apply | Module visible to the caller; version exists and is active; every required input present and no undeclared input | [D] [i-mod][i-mod] |
| Execution | On a worker, in the background, with progress reported | [D] [i-mod][i-mod] |
| Update and upgrade | Inputs change in place; an upgrade runs the new version's code against existing state and carries inputs forward, asking only for new required ones; the target version must be active | [D] [i-mod][i-mod] |
| Fixed module | A deployment cannot switch to a different module | [D] [i-mod][i-mod] |
| Registry guard | A module or version in use by a deployment cannot be deleted from the registry; yanking is allowed and a deployment pinned to a yanked version can still be deleted | [D] [i-mod][i-mod] |
| Shared modules | Revoking a share can leave a deployment that cannot be deleted until access returns | [D] [i-mod][i-mod] |
| Inner resources | `intentModuleResources(intentProjectID!, resourceID!, first, after)`: walks the module's state, excludes data sources, at most 50 per page | [G] |
| Permissions | Read on the module and the right to operate in the project's space | [D] [i-mod][i-mod] |
| Release | Self-hosted v6.2.0 | [RN] [sh-cl][sh-cl] |

### 5.12 Access control

28 `Action` values start with `INTENT_` [G], matching the 28 Intent actions in
the RBAC catalogue, each with legacy fallback Admin [D] [rbac][rbac]. Space
admins can manage Intent projects in their spaces by default [D]
[i-exec][i-exec].

| `Action` | Catalogue name |
|---|---|
| `INTENT_DEPENDENCIES_ADD`, `INTENT_DEPENDENCIES_REMOVE` | Add / Remove intent dependencies |
| `INTENT_POLICY_CREATE`, `INTENT_POLICY_UPDATE`, `INTENT_POLICY_DELETE` | Create / Update / Delete intent policies |
| `INTENT_PROJECT_CLOUD_INTEGRATION_ATTACH`, `INTENT_PROJECT_CLOUD_INTEGRATION_DETACH` | Attach / Detach AWS integration to intent project |
| `INTENT_PROJECT_CONFIG_ADD`, `INTENT_PROJECT_CONFIG_UPDATE`, `INTENT_PROJECT_CONFIG_DELETE` | Add / Update / Delete intent project config |
| `INTENT_PROJECT_CREATE`, `INTENT_PROJECT_UPDATE`, `INTENT_PROJECT_DELETE` | Create / Update / Delete intent project |
| `INTENT_PROJECT_ENABLE`, `INTENT_PROJECT_DISABLE` | Enable / Disable intent project |
| `INTENT_PROJECT_LOCK`, `INTENT_PROJECT_UNLOCK` | Lock / Unlock intent project |
| `INTENT_PROJECT_POLICY_ATTACH`, `INTENT_PROJECT_POLICY_DETACH` | Attach / Detach policy to intent project |
| `INTENT_RESOURCE_CREATE`, `INTENT_RESOURCE_UPDATE`, `INTENT_RESOURCE_DELETE` | Create / Update / Delete intent resources |
| `INTENT_RESOURCE_IMPORT`, `INTENT_RESOURCE_REFRESH`, `INTENT_RESOURCE_RESUME` | Import / Refresh / Resume intent resources |
| `INTENT_RESOURCE_OPERATION_REVIEW` | Review intent resource operation |
| `INTENT_STATE_EJECT`, `INTENT_STATE_READ` | Eject from intent state; Read intent state (also data source reads) |

The Intent policy actions are separate from the general policy actions, so
policy authors and resource operators can be different people [D]
[intent][intent]. There is no action for reading a project; read access
presumably follows space read access *(unverified)*.

## 6. Intent policies

The `INTENT` type evaluates every create, update, delete, import and refresh
in an Intent project or in Build mode [D] [ip][ip]. Report
[09](09-policies.md) places it among the policy types.

| Outcome | Condition | Source |
|---|---|---|
| Allowed | At least one `allow` matched and no `deny` | [D] [ip][ip] |
| Denied | Any `deny` matched; reasons are returned to the user | [D] [ip][ip] |
| Pending review | Neither matched; waits for manual approval | [D] [ip][ip] |
| No policy attached | Everything allowed | [D] [ip][ip] |
| Several policies | Evaluated independently and combined; one `deny` from any blocks | [D] [ip][ip] |

Rules: `allow` and `deny` (string sets), plus the common `flag`, `reset_flag`
and `sample` [S] [contract][contract].

Input, from the policy contract [S] [contract][contract]:

| Path | Type | Required | Meaning |
|---|---|---|---|
| `resource.resource_id` | string | yes | Resource ID |
| `resource.provider` | string | yes | Provider, such as `hashicorp/aws`; for a module, the module slug [D] [i-mod][i-mod] |
| `resource.provider_version` | string | no | Semver; for a module, the exact version |
| `resource.resource_type` | string | yes | Such as `aws_instance`, or `spacelift/module` |
| `resource.operation` | string | yes | `create`, `update`, `delete`, `refresh`, `import` |
| `resource.current_state` | object or null | no | Provider-defined keys; null on create |
| `resource.proposed_state` | object or null | no | Provider-defined keys; null on delete; for a module, the inputs supplied |
| `reviews.allow[]`, `reviews.deny[]` | `intent_review` | no | Prior decisions: `author`, and the `request` and `session` documents as stored at review time, whose keys follow whichever version wrote them |

- For a module deployment the policy sees inputs, not the expanded plan [D]
  [i-mod][i-mod].
- `intentPolicyInputSchema: String!` returns the input schema at run time [G].
- Auto-attachment: `autoattach:<label>` matches Intent projects with that
  label, and `autoattach:*` matches every project, within the policy's
  inheritable space chain [D] [ip][ip].
  - An explicit attachment wins over an auto-attachment, and clears the
    auto-attached marker [D] [ip][ip].
  - `isAutoattached` appears on both attachment types [G].
  - Auto-attachment was added on 2026-05-08 [RN] [cl][cl].
- Attachments: `IntentProjectPolicyAttachment` has `id`, `policyId`,
  `policyBody`, `policyName`, `policyType`, `policyLabels`,
  `evaluationRecords`, `evaluationSample(key)` and `isAutoattached` [G].
- The policy side, `PolicyIntentProjectAttachment`, has `id`, `projectID`,
  `projectName`, `policyID`, `policyName`, `projectSpaceDetails` and
  `isAutoattached`; `Policy.attachedIntentProjects` and
  `Policy.hasAttachedIntentProjects` expose the attachments [G].
- `sample` captures evaluations for the policy workbench [D] [i-ts][i-ts].
  `INTENT` has no policy input capture kind (report 09).

## 7. The Spacelift MCP server

### 7.1 Endpoint and transport

| Item | Detail | Source |
|---|---|---|
| URL | `https://<account>.app.spacelift.io/mcp`, or `https://<account>.app.us.spacelift.io/mcp` for US accounts; self-hosted `https://<your-domain>/mcp` | [D] [mcp][mcp]; [RN] [sh-cl][sh-cl] v6.0.0 |
| Transport | Remote HTTP; clients are configured with `type: http` (Claude Code, VS Code), `httpUrl` (Gemini), `url` (Cursor, Codex) | [D] [mcp-c][mcp-c] |
| Clients named | Claude Code, Claude Desktop, Cursor, VS Code, Codex, Gemini, ChatGPT; any MCP-capable client should work | [D] [mcp][mcp]; [D] [i-ts][i-ts]; [D] [intent][intent] |
| Former endpoint | `/intent/mcp`, merged into `/mcp` on 2026-04-13 and announced for removal on 2026-08-01 | [D] [mcp][mcp]; [RN] [cl][cl] |
| Claude Code plugin | `spacelift-io/claude-plugins` adds a `spacelift` skill that guides tool choice | [D] [mcp][mcp] |

Whether `/intent/mcp` was removed on schedule is not recorded in the
changelog *(unverified)*.

### 7.2 Tools

| Tool | Purpose | Scope needed | Source |
|---|---|---|---|
| `discover` | Browse the GraphQL schema: queries, mutations, types, fields and required arguments; always present and cannot be removed | `mcp:read` | [D] [mcp][mcp]; [D] [mcp-c][mcp-c] |
| `query` | Run read-only GraphQL queries | `mcp:read` | [D] [mcp][mcp] |
| `provider` | Look up schemas for providers, resources and data sources, sourced from the OpenTofu registry; read-only | `mcp:read` | [D] [mcp][mcp] |
| `mutate` | Run GraphQL mutations, including creating and managing Intent projects, policies and cloud integrations | `mcp:write` | [D] [mcp][mcp]; [D] [intel][intel] |
| `intent` | Intent lifecycle: `create`, `update`, `delete`, `refresh`, `import`, `resume`, `read`, `status`, plus module deployment; every write checked against Intent policies | `mcp:write` | [D] [intel][intel]; [D] [mcp][mcp] |

Asking for `intent` also enables `provider` [D] [mcp][mcp]. The input schemas
of the five tools are not published; the docs only say that `provider` and
`intent` carry long, multi-paragraph descriptions [D] [mcp-c][mcp-c].
*(unverified)*

### 7.3 Authentication

| Method | Detail | Source |
|---|---|---|
| Browser OAuth (default) | The client opens a consent page; afterwards the client keeps a short-lived token that it renews | [D] [mcp-c][mcp-c] |
| Scopes | `mcp:read` grants `discover`, `query`, `provider`; `mcp:write` adds `mutate` and `intent` and implies `mcp:read`; both are pre-selected; widening requires re-authentication | [D] [mcp-c][mcp-c] |
| Scope history | The single `mcp` scope was split on 2026-05-14 and removed without an alias, forcing re-authentication | [RN] [cl][cl] |
| OAuth endpoints | Discovery, client registration, authorization and token endpoints are served (fixed for self-hosted in v6.1.0 and v6.1.1) | [RN] [sh-cl][sh-cl] |
| Redirect hosts | Loopback (`localhost`, `127.0.0.1`, `::1`) and a built-in set of recognized client hostnames; an account admin can add hosts under **Organization settings** > _OAuth redirect allowlist_ | [D] [mcp-c][mcp-c]; [D] [sec][sec] |
| Allowlist entries | A bare hostname, or one leading `*.` that matches subdomains but not the apex; additive to the built-in list; only extends `https` hosts, while plain `http` stays loopback-only | [D] [mcp-c][mcp-c] |
| Bearer token | `spacectl profile export-token` output sent as `Authorization: Bearer ...`; short-lived; not narrowed by scopes, so it carries the user's or API key's full access | [D] [mcp-c][mcp-c] |
| Authorization | RBAC and login policies decide what any session may do | [D] [mcp][mcp] |

The registration endpoint means clients register dynamically, as the MCP
authorization specification provides [I] from [sh-cl][sh-cl] v6.1.1. A
refused registration returns an error naming the unsupported redirect host
[D] [mcp-c][mcp-c]. The endpoint paths are not documented *(unverified)*.

### 7.4 The `tools` query parameter

- **What it is.** A comma-separated list appended to the URL, such as
  `?tools=query,provider`. It is Alpha, added on 2026-06-01 [D]
  [mcp-c][mcp-c]; [RN] [cl][cl].
- **Accepted names.** `query`, `mutate`, `provider`, `intent` [D]
  [mcp-c][mcp-c].
- **Narrowing only.** The selection is intersected with what the scope and
  deployment allow, and never widens it [D] [mcp-c][mcp-c].
- **Implied tools.** `intent` brings `provider` with it, and `discover` is
  always kept [D] [mcp-c][mcp-c].
- **Unknown names.** They are ignored. If every name is unknown, the parameter
  has no effect [D] [mcp-c][mcp-c].
- **Authorization.** Per-action RBAC still applies when a tool runs [D]
  [mcp-c][mcp-c].
- **Purpose.** It cuts per-turn tokens and improves tool choice. It is the only
  narrowing control for API-key and `spacectl`-token callers [D]
  [mcp-c][mcp-c].

## 8. The open-source `spacelift-intent`

`spacelift-io/spacelift-intent` (Apache-2.0) is a standalone MCP server. The
README calls it experimental and points to the managed version on the
Spacelift platform for policies and auditing [S] [si-readme][si-readme]. It is
not the code behind `/mcp`. Its tools, storage and provider handling are the
nearest public evidence of how Intent drives providers [I] from the README and
the tool names.

### 8.1 What it runs

| Item | Detail | Source |
|---|---|---|
| Binary | `spacelift-intent` (Go 1.25.1 module), built for Linux, macOS and Windows on amd64 and arm64; Docker image `ghcr.io/spacelift-io/spacelift-intent` on Alpine; Homebrew formula | [S] [si-gomod][si-gomod]; [S] [si-gr][si-gr]; [S] [si-df][si-df]; [S] [si-readme][si-readme] |
| MCP SDK | `github.com/modelcontextprotocol/go-sdk` v1.4.1; server name `spacelift-intent`, version string `1.0.0`; server instructions embedded from `instructions/claude-instructions.md` | [S] [si-srv][si-srv] |
| Transport | stdio only | [S] [si-srv][si-srv] |
| State | SQLite (`modernc.org/sqlite`) at `<db-dir>/state.db`, migrated on start with golang-migrate | [S] [si-main][si-main]; [S] [si-gomod][si-gomod] |
| Flags | `--tmp-dir` / `TMP_DIR` (default `/tmp/spacelift-intent-executor`, provider binaries); `--db-dir` / `DB_DIR` (default `./.state/`) | [S] [si-flags][si-flags] |
| Registry overrides | `OPENTOFU_REGISTRY_URL` (default `https://registry.opentofu.org`), `OPENTOFU_API_URL` (default `https://api.opentofu.org`) | [S] [si-reg][si-reg] |
| Shutdown | SIGINT or SIGTERM, then a 30-second graceful stop that closes provider plugins and the database | [S] [si-main][si-main] |

### 8.2 How it talks to providers and clouds

- **Registry.** Providers are found and fetched from the OpenTofu registry
  [S] [si-reg][si-reg]:
  - search: `GET {api}/registry/docs/search?q=`;
  - versions: `GET {registry}/v1/providers/{ns}/{name}/versions`;
  - download: `GET {registry}/v1/providers/{ns}/{name}/{version}/download/{os}/{arch}`;
  - every request sends the user agent `spacelift-intent`.
- **Unpacking.** The provider zip is unpacked under `TMP_DIR/<ns>_<name>@<version>`
  and the binary marked executable [S] [si-dl][si-dl].
- **No checksum.** The registry's `shasum` is read but never compared [S]
  [si-reg][si-reg], so the download is not verified [I] from a search of the
  module for its use.
- **Plugin protocol.** Each provider runs as a gRPC plugin through
  `github.com/opentofu/provider-client`, speaking Terraform plugin protocol 5.9
  [S] [si-load][si-load]; [S] [si-proto][si-proto].
- **Provider configuration.** The provider is configured with an empty config
  first, then with every attribute null (some providers, such as `azurerm`,
  require a block to exist), always reporting Terraform version `1.6.0` [S]
  [si-conf][si-conf].
- **Cloud credentials.** They therefore come from the process environment,
  such as `AWS_ACCESS_KEY_ID` and `AWS_REGION`, as with Terraform [S]
  [si-readme][si-readme].
- **Create.** It is `PlanManagedResourceChange` followed by
  `ApplyManagedResourceChange` [S] [si-create][si-create].
- **Other operations.**
  - update plans and applies again;
  - delete plans to a null state and applies;
  - import calls `ImportManagedResourceState` and then `ReadManagedResource`;
  - refresh calls `ReadManagedResource`;
  - data sources call `ReadDataSource`.

  All are in [S] [si-ad][si-ad].
- **Update merge.** An update merges the new config over the stored state at
  the top level only, so nested maps (such as tags) are replaced whole and
  items can be removed [S] [si-merge][si-merge].
- **No Spacelift connection.** The server does not call Spacelift. Its tests
  manage Spacelift objects through the `spacelift-io/spacelift` provider with
  `SPACELIFT_API_KEY_ENDPOINT`, `SPACELIFT_API_KEY_ID` and
  `SPACELIFT_API_KEY_SECRET`, as a cloud like any other [S] [si-test][si-test].
- **No policy evaluation.** The `operations` table has `allow` and `deny`
  columns and tool descriptions mention policies, but nothing writes them [I]
  from a search for policy code; [S] [si-mig][si-mig].

### 8.3 MCP tools

18 tools, enumerated by script from the source [S]
[si-h][si-h]. Hints are MCP tool annotations (`readOnlyHint`,
`destructiveHint`, `idempotentHint`, `openWorldHint`) [S] [si-ann][si-ann].

| Tool | File | Hints | Inputs (required in bold) |
|---|---|---|---|
| `provider-search` | [search.go#L24][si-t-ps] | read-only, idempotent, open-world | **`query`** |
| `provider-describe` | [describe.go#L30][si-t-pd] | read-only, idempotent | **`provider`**, **`provider_version`** |
| `provider-resources-describe` | [describe.go#L31][si-t-prd] | read-only, idempotent | **`provider`**, **`resource_type`**, **`provider_version`** |
| `provider-datasources-describe` | [describe.go#L31][si-t-pdd] | read-only, idempotent | **`provider`**, **`data_source_type`**, **`provider_version`** |
| `lifecycle-resources-create` | [create.go#L35][si-t-c] | open-world | **`resource_id`**, **`provider`**, **`resource_type`**, **`config`**, **`provider_version`** |
| `lifecycle-resources-update` | [update.go#L24][si-t-u] | idempotent, open-world | **`resource_id`**, **`config`** |
| `lifecycle-resources-delete` | [delete.go#L23][si-t-d] | destructive, idempotent, open-world | **`resource_id`** |
| `lifecycle-resources-refresh` | [refresh.go#L24][si-t-r] | idempotent, open-world | **`resource_id`** |
| `lifecycle-resources-import` | [import.go#L35][si-t-i] | idempotent, open-world | **`import_id`**, **`destination_id`**, **`provider`**, **`resource_type`**, **`provider_version`** |
| `lifecycle-resources-operations` | [operations.go#L33][si-t-o] | open-world | `resource_id`, `provider`, `provider_version`, `resource_type`, `page`, `page_size` (default 20, max 100) |
| `lifecycle-datasources-read` | [read.go#L34][si-t-dr] | read-only, idempotent, open-world | **`provider`**, **`data_source_type`**, **`config`**, **`provider_version`** |
| `lifecycle-resources-dependencies-add` | [add.go#L18][si-t-da] | idempotent | **`from_resource_id`**, **`to_resource_id`**, **`dependency_type`** (`explicit`, `implicit`, `data_source`), `explanation`, `field_mappings[]` (`source_field`, `target_field` required; `description`) |
| `lifecycle-resources-dependencies-get` | [get.go#L18][si-t-dg] | read-only | **`resource_id`**, `direction` |
| `lifecycle-resources-dependencies-remove` | [remove.go#L23][si-t-dx] | destructive, idempotent | **`from_resource_id`**, **`to_resource_id`** |
| `state-get` | [get.go#L22][si-t-sg] | read-only, idempotent | **`resource_id`** |
| `state-list` | [list.go#L19][si-t-sl] | read-only, idempotent | none |
| `state-eject` | [eject.go#L22][si-t-se] | destructive, idempotent | **`resource_id`** |
| `state-timeline` | [timeline.go#L18][si-t-st] | read-only, idempotent | `resource_id`, `from_time`, `to_time`, `limit` (default 50), `offset` (default 0) |

- The create handler's argument struct also has `provider_config`, but the
  advertised input schema omits it [S] [si-t-c][si-t-c].
- Create and import refuse an ID that already exists [S] [si-t-c][si-t-c].
- Delete does not check dependents in code: the guard is only in the tool
  description and the embedded instructions, and edges cascade away with the
  state row [S] [si-del][si-del]; [S] [si-mig][si-mig].
- The embedded instructions tell the model to ask for an explicit `CONFIRM`
  before any delete and to run `state-list` first in a session [S]
  [si-instr][si-instr].
- The `dependency_type` values map to `IntentResourceDependencyType` in the
  hosted schema [I] from the identical names; [G].

### 8.4 Storage schema

[S] [si-mig][si-mig]:

| Table | Columns | Notes |
|---|---|---|
| `state_records` | `id` (PK), `provider`, `provider_version` (renamed from `version`), `resource_type`, `state` (JSON), `created_at` | Index on `(provider, resource_type)` and `created_at` |
| `dependency_edges` | `from_resource_id`, `to_resource_id` (composite PK, both FK to `state_records` with cascade delete), `dependency_type`, `explanation`, `field_mappings` (JSON, default `[]`), `created_at` | |
| `timeline_events` | `id`, `resource_id`, `operation`, `changed_by`, `created_at` | Deletes record `changed_by = mcp-user` [S] [si-del][si-del] |
| `operations` | `id` (UUIDv7), `created_at`, `allow`, `deny`, `failed`, `resource_id`, `resource_type`, `provider`, `provider_version`, `operation`, `current_state`, `proposed_state` | One row per lifecycle tool call |

The `operations` columns match `IntentResourceOperation`'s fields one for one,
and `timeline_events` matches the project **History** tab [I] from comparing
[si-mig][si-mig] with [G] and [i-wp][i-wp].

### 8.5 Hosted and open-source compared

| Aspect | Hosted Intent (`/mcp`) | `spacelift-intent` | Source |
|---|---|---|---|
| Tools | One `intent` tool with an operation argument, plus `provider` | 18 separate tools | [D] [mcp][mcp]; [S] [si-h][si-h] |
| Transport | Remote HTTP with OAuth | Local stdio | [D] [mcp-c][mcp-c]; [S] [si-srv][si-srv] |
| Execution | On a worker pool with an Intent image | In the MCP server process | [D] [i-exec][i-exec]; [S] [si-load][si-load] |
| State | Spacelift-managed and encrypted, per project | One SQLite file, no projects | [D] [i-mod][i-mod]; [S] [si-main][si-main] |
| Policies and reviews | `INTENT` policies, manual review | None | [D] [ip][ip]; [I] |
| Locks, RBAC, audit | Yes | No | [D] [i-wp][i-wp] |
| Credentials | Cloud integrations or project environment | Process environment | [D] [i-cred][i-cred]; [S] [si-readme][si-readme] |
| Modules | `spacelift/module` from the registry | No | [D] [i-mod][i-mod] |
| Export to HCL | Yes | No | [D] [i-iac][i-iac] |

## What this means for infraweave

- **Two AI surfaces on one toolset.** An in-product assistant and an external
  MCP endpoint must share one tool layer and one policy gate, so a capability
  added to one reaches the other.
- **AI integrations.**
  - BYOM support covers four providers: Anthropic, OpenAI and Google with keys,
    and Bedrock through a cloud integration.
  - Base URLs point at gateways and must be `https`.
  - Saving runs live model discovery and a test completion.
  - Integrations are space-scoped, can be toggled, and encrypt keys at rest.
  - A server-side proxy holds the keys and enforces the enabled flag.
  - A Spacelift-provided default and its token budget have no self-hosted
    equivalent, so the plan must state what infraweave offers by default.
- **Account toggles.** Intelligence, assistant and Build mode are switched on
  separately and gated by acceptance of the terms. A non-admin can ask an
  admin to enable them.
- **Intent engine.**
  - Projects carry a lock with an inactivity timeout, a per-caller active
    project, a `READ_WRITE` / `READ_ONLY` state, environment and file
    configuration, a worker pool and a runner image.
  - Resources keep JSON state and HCL. Operations follow a six-state
    lifecycle with manual review and resume.
  - Dependencies come in three types and block teardown.
  - Execution reuses the run worker path, and long operations are
    asynchronous with status polling.
- **Provider driving.** Intent calls provider plugins directly over the
  Terraform plugin protocol, as `spacelift-intent` shows with
  `opentofu/provider-client`. Downloads need registry access and a checksum
  policy.
- **The `INTENT` policy type.** Its input carries the resource operation and
  prior reviews, and an evaluation can end in a third outcome, pending review,
  besides allow and deny. Auto-attach works by label.
- **Intent to IaC.** Export generates HCL with references, creates a repo and
  an OpenTofu stack, copies environment and integrations, imports state and
  locks the project read-only.
- **Registry modules** deploy as a single resource, with input validation,
  in-place upgrades and a registry delete guard.
- **MCP server.**
  - Remote HTTP transport, OAuth with dynamic client registration, two scopes
    and a redirect-host allowlist.
  - Bearer-token access, and a `tools` narrowing parameter.
  - A discover/query/mutate trio over infraweave's own API. That API is REST,
    not GraphQL, so the trio's shape is a design question for later tickets.
- **Conversations** are stored with versioning, a model choice per
  conversation and an exportable transcript. Run summaries go through the
  assistant.
- **Audit and RBAC.** There are three audit resource types and 32 actions
  (28 for Intent, 4 for AI integrations).

## Inferred and undocumented

- **[I] An AI provider proxy.** The server holds the provider keys and routes
  every call through a proxy that enforces the enabled flag. Inferred from the
  `AIIntegration.enabled` description [G] and from keys never being shown [D].
- **[I] The chat channel is outside GraphQL.** It also accounts tokens against
  the budget. Inferred because the schema has no send-message or streaming
  operation [G].
- **[I] Operation state transitions (section 5.5).** Inferred from the enum
  descriptions [G] and the policy outcomes [D]. The terminal state after a
  deny is *(unverified)*.
- **[I] Intent executions use the run launcher path and receive an OIDC token.**
  Inferred from [i-exec][i-exec] and the `spacelift.oidc` mount in
  [i-cred][i-cred].
- **[I] Hosted Intent descends from `spacelift-intent`.** Inferred from the
  matching dependency types, the operation fields and the README's pointer to
  the managed version.
- **[I] MCP clients register dynamically.** Inferred from the registration
  endpoint in the self-hosted v6.1.1 notes.
- **Docs conflict on Bedrock.** The 2026-04-13 changelog says Bedrock BYOM
  serves both Infra Assistant and Intent [RN]. The AI integrations page says
  Intent never uses AI integrations [D]. The docs page is the higher source
  and wins.
- **Bruno and the schema differ on provider strings.** Bruno uses `OPENAI`
  [B], while the schema description and the Terraform provider use `OpenAI`
  [G] [S]. The schema wins.
- **Unverified items:**
  - `ACCOUNT_AI_CONTROL`'s meaning;
  - Intent's release status;
  - `keepResources` semantics;
  - `intentProjectDisable` setting `READ_ONLY`;
  - project read access;
  - whether Build mode exposes general mutations;
  - the hosted tools' input schemas;
  - the default Intent worker image and launcher message;
  - the OAuth endpoint paths;
  - whether `/intent/mcp` is gone;
  - upper-case provider values;
  - how Spacelift-hosted AI credits relate to the token budget.

## Versions at the research baseline

| Component | Version | Note |
|---|---|---|
| Spacelift SaaS GraphQL schema | introspection `b3255fe38f5e` | 12 AI and 13 Intent queries (3 deprecated), 8 AI and 24 Intent mutations, counted by script |
| Self-hosted Spacelift | v7.1.0 (`latest`) | Intelligence since v6.0.0; run summaries since v7.0.0 |
| spacelift-api-bruno | `7cad4574954b` | 36 requests marked as an Intent and AI chat preview |
| terraform-provider-spacelift | v1.57.0 (`c72b9bd2f3c3`) | `spacelift_ai_integration` resource and two data sources |
| spacelift-intent | `03a4865d888b` (2026-09-24) | Apache-2.0; MCP go-sdk v1.4.1; plugin protocol 5.9 |
| Spacelift-provided models | Claude Sonnet 4.6, Claude Opus 4.8 on Bedrock | Per [aii][aii] |
| AI Terms of Use | last updated 2026-09-08 | [ai-terms][ai-terms] |
| Intent early-access terms | effective 2025-10-08 | [intent-terms][intent-terms] |

## Sources

Documentation (read 2026-10-28):
- Spacelift Intelligence, <https://docs.spacelift.io/concepts/intelligence>
- AI integrations, <https://docs.spacelift.io/concepts/intelligence/ai-integrations>
- Infra Assistant, <https://docs.spacelift.io/concepts/intelligence/infra-assistant>
- Spacelift Intent, <https://docs.spacelift.io/concepts/intelligence/intent>
- Get started with Intent, <https://docs.spacelift.io/concepts/intelligence/intent/get-started>
- Working with projects, <https://docs.spacelift.io/concepts/intelligence/intent/working-with-projects>
- Execution and access control, <https://docs.spacelift.io/concepts/intelligence/intent/execution>
- Intent to IaC, <https://docs.spacelift.io/concepts/intelligence/intent/intent-to-iac>
- Deploying Spacelift modules, <https://docs.spacelift.io/concepts/intelligence/intent/spacelift-modules>
- Azure and GCP credentials, <https://docs.spacelift.io/concepts/intelligence/intent/setting-up-azure-and-gcp-credentials>
- Troubleshooting and FAQ, <https://docs.spacelift.io/concepts/intelligence/intent/troubleshooting>
- Handy commands and prompts, <https://docs.spacelift.io/concepts/intelligence/intent/handy-commands-and-prompts>
- Spacelift MCP, <https://docs.spacelift.io/concepts/intelligence/spacelift-mcp>
- Connecting to the MCP server, <https://docs.spacelift.io/concepts/intelligence/spacelift-mcp/connecting>
- Intent policy, <https://docs.spacelift.io/concepts/policy/intent-policy>
- RBAC system, <https://docs.spacelift.io/concepts/authorization/rbac-system>
- Run summaries, <https://docs.spacelift.io/concepts/run/summarization>
- Spacelift Intelligence Terms of Use, <https://docs.spacelift.io/legal/ai-terms>
- Spacelift Intent Early Access Release, <https://docs.spacelift.io/legal/intent-terms>
- Security, <https://docs.spacelift.io/product/security>
- Support (alpha features), <https://docs.spacelift.io/product/support>
- Self-hosted Spacelift Intelligence, <https://docs.spacelift.io/self-hosted/latest/concepts/intelligence>
- Self-hosted AI integrations, <https://docs.spacelift.io/self-hosted/latest/concepts/intelligence/ai-integrations>

Changelogs:
- <https://docs.spacelift.io/product/changelog>, entries 2026-04-13, 2026-05-08, 2026-05-14, 2026-06-01, 2026-06-23, 2026-07-16, 2026-08-21, 2026-08-27
- <https://docs.spacelift.io/self-hosted/latest/installing-spacelift/changelog>, v6.0.0 to v7.0.0

Schemas and snapshots:
- `api/reference/spacelift/introspection.json` (`b3255fe38f5e`), `bruno.json` (`7cad4574954b`), `terraform-schema.json` (v1.57.0)
- Policy contract, <https://app.spacelift.io/.well-known/policy-contract.json>, `policyTypes.INTENT` and `$defs.intent_review`, read 2026-10-28

Source code:
- <https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_ai_integration.go#L17-L47>
- <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/README.md>
- <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/go.mod#L1-L15>
- <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/.goreleaser.yml#L6-L15>
- <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/Dockerfile>
- <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/cmd/spacelift-intent/server.go#L48-L81>
- <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/cmd/spacelift-intent/main.go#L30-L80>
- <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/cmd/spacelift-intent/flags.go#L8-L21>
- <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/registry/opentofu.go#L20-L60>
- <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/provider/opentofu_adapter.go#L157-L189>
- <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/provider/opentofu_adapter.go#L83-L119>
- <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/provider/opentofu_adapter.go#L415-L490>
- <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/provider/opentofu_adapter.go#L519-L910>
- <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/provider/opentofu_adapter.go#L846-L856>
- <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/provider/opentofu_adapter.go#L1058-L1118>
- <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/proto/tfplugin5.proto#L4>
- <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/handlers.go#L35-L89>
- <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/internal/annotation.go#L13-L38>
- <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/lifecycle/resources/delete.go#L45-L110>
- <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/storage/migrations/1728565100_initial_schema.up.sql#L1-L61>
- <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/instructions/claude-instructions.md>
- <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/test/testhelper/spacelift_api.go#L114-L125>
- Tool definitions, one link per tool in section 8.3:
  - <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/provider/search.go#L24>
  - <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/provider/describe.go#L30>
  - <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/provider/resources/describe.go#L31>
  - <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/provider/datasources/describe.go#L31>
  - <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/lifecycle/resources/create.go#L16-L90>
  - <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/lifecycle/resources/update.go#L24>
  - <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/lifecycle/resources/delete.go#L23>
  - <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/lifecycle/resources/refresh.go#L24>
  - <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/lifecycle/resources/import.go#L35>
  - <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/lifecycle/resources/operations.go#L33>
  - <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/lifecycle/datasources/read.go#L34>
  - <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/lifecycle/resources/dependencies/add.go#L18>
  - <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/lifecycle/resources/dependencies/get.go#L18>
  - <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/lifecycle/resources/dependencies/remove.go#L23>
  - <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/state/get.go#L22>
  - <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/state/list.go#L19>
  - <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/state/eject.go#L22>
  - <https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/state/timeline.go#L18>

Tables of types, root fields, enums, counts, the Terraform resource and the
18 `spacelift-intent` tools were generated by script from the sources above.

[intel]: https://docs.spacelift.io/concepts/intelligence
[aii]: https://docs.spacelift.io/concepts/intelligence/ai-integrations
[ia]: https://docs.spacelift.io/concepts/intelligence/infra-assistant
[intent]: https://docs.spacelift.io/concepts/intelligence/intent
[i-gs]: https://docs.spacelift.io/concepts/intelligence/intent/get-started
[i-wp]: https://docs.spacelift.io/concepts/intelligence/intent/working-with-projects
[i-exec]: https://docs.spacelift.io/concepts/intelligence/intent/execution
[i-iac]: https://docs.spacelift.io/concepts/intelligence/intent/intent-to-iac
[i-mod]: https://docs.spacelift.io/concepts/intelligence/intent/spacelift-modules
[i-cred]: https://docs.spacelift.io/concepts/intelligence/intent/setting-up-azure-and-gcp-credentials
[i-ts]: https://docs.spacelift.io/concepts/intelligence/intent/troubleshooting
[i-hc]: https://docs.spacelift.io/concepts/intelligence/intent/handy-commands-and-prompts
[mcp]: https://docs.spacelift.io/concepts/intelligence/spacelift-mcp
[mcp-c]: https://docs.spacelift.io/concepts/intelligence/spacelift-mcp/connecting
[ip]: https://docs.spacelift.io/concepts/policy/intent-policy
[rbac]: https://docs.spacelift.io/concepts/authorization/rbac-system
[sum]: https://docs.spacelift.io/concepts/run/summarization
[ai-terms]: https://docs.spacelift.io/legal/ai-terms
[intent-terms]: https://docs.spacelift.io/legal/intent-terms
[sec]: https://docs.spacelift.io/product/security
[support]: https://docs.spacelift.io/product/support
[cl]: https://docs.spacelift.io/product/changelog
[sh-intel]: https://docs.spacelift.io/self-hosted/latest/concepts/intelligence
[sh-aii]: https://docs.spacelift.io/self-hosted/latest/concepts/intelligence/ai-integrations
[sh-cl]: https://docs.spacelift.io/self-hosted/latest/installing-spacelift/changelog
[contract]: https://app.spacelift.io/.well-known/policy-contract.json
[tf-ai]: https://github.com/spacelift-io/terraform-provider-spacelift/blob/c72b9bd2f3c3/spacelift/resource_ai_integration.go#L17-L47
[si-readme]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/README.md
[si-gomod]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/go.mod#L1-L15
[si-gr]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/.goreleaser.yml#L6-L15
[si-df]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/Dockerfile
[si-srv]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/cmd/spacelift-intent/server.go#L48-L81
[si-main]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/cmd/spacelift-intent/main.go#L30-L80
[si-flags]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/cmd/spacelift-intent/flags.go#L8-L21
[si-reg]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/registry/opentofu.go#L20-L60
[si-load]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/provider/opentofu_adapter.go#L157-L189
[si-conf]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/provider/opentofu_adapter.go#L83-L119
[si-create]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/provider/opentofu_adapter.go#L415-L490
[si-ad]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/provider/opentofu_adapter.go#L519-L910
[si-merge]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/provider/opentofu_adapter.go#L846-L856
[si-dl]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/provider/opentofu_adapter.go#L1058-L1118
[si-proto]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/proto/tfplugin5.proto#L4
[si-h]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/handlers.go#L35-L89
[si-ann]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/internal/annotation.go#L13-L38
[si-del]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/lifecycle/resources/delete.go#L45-L110
[si-mig]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/storage/migrations/1728565100_initial_schema.up.sql#L1-L61
[si-instr]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/instructions/claude-instructions.md
[si-test]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/test/testhelper/spacelift_api.go#L114-L125
[si-t-ps]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/provider/search.go#L24
[si-t-pd]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/provider/describe.go#L30
[si-t-prd]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/provider/resources/describe.go#L31
[si-t-pdd]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/provider/datasources/describe.go#L31
[si-t-c]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/lifecycle/resources/create.go#L16-L90
[si-t-u]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/lifecycle/resources/update.go#L24
[si-t-d]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/lifecycle/resources/delete.go#L23
[si-t-r]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/lifecycle/resources/refresh.go#L24
[si-t-i]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/lifecycle/resources/import.go#L35
[si-t-o]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/lifecycle/resources/operations.go#L33
[si-t-dr]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/lifecycle/datasources/read.go#L34
[si-t-da]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/lifecycle/resources/dependencies/add.go#L18
[si-t-dg]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/lifecycle/resources/dependencies/get.go#L18
[si-t-dx]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/lifecycle/resources/dependencies/remove.go#L23
[si-t-sg]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/state/get.go#L22
[si-t-sl]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/state/list.go#L19
[si-t-se]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/state/eject.go#L22
[si-t-st]: https://github.com/spacelift-io/spacelift-intent/blob/03a4865d888b/tools/state/timeline.go#L18
