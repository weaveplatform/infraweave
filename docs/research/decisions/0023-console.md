# 0023: Console

Status: Accepted

## Context

infraweave mirrors every capability a Spacelift user reaches through the web
interface ([0021](0021-capability-scope-and-supersession.md)), so the console
covers every Spacelift UI surface. An inventory of those surfaces counts 30,
in six classes:

| Class | Count | Surfaces, with the report that records them |
|---|---|---|
| Tables, lists, detail pages and forms | 15 | Stacks, runs, policies, contexts, modules, spaces, users and the other entities of reports 07 to 25 |
| Live streams | 4 | Run phase logs and task output ([08 section 10](../reference/08-run-lifecycle.md#10-log-retention), [06 section 3.3](../reference/06-spacectl-and-clients.md#33-stack)); Flows live event counters and the invocations sidebar ([23 section 4](../reference/23-flows.md#4-events-invocations-and-limits)) |
| Graphs and canvases | 3 | The Flows canvas ([23 section 3](../reference/23-flows.md#3-flows-and-blocks)), the stack dependency graph ([18 section 4](../reference/18-scheduling-drift-dependencies.md#4-stack-dependencies)) and the spaces tree ([14 section 2](../reference/14-spaces-rbac-and-identity.md#2-spaces)) |
| Code editors | 4 | The plan's changes view ([21 section 3](../reference/21-resources-scans-search.md#3-entity-changes)), the Rego policy workbench ([09 section 6](../reference/09-policies.md#6-workbench-simulation-captures-and-templates)), the blueprint and template workbench ([17 section 5](../reference/17-blueprints-and-templates.md#5-the-blueprint-body)) and the Flows expression editor ([23 section 3.4](../reference/23-flows.md#34-expressions-and-event-data)) |
| Chat | 1 | Infra Assistant, which navigates the interface and reads the current page ([22 section 4.1](../reference/22-intelligence.md#41-modes-and-tools)) |
| Other | 3 | Dashboards with user-arranged widgets ([21 section 9](../reference/21-resources-scans-search.md#9-dashboard)), the Flows dashboard, and app-supplied UI ([23 section 7.4](../reference/23-flows.md#74-services)) |

Facts from the same inventory that shape the design:

- Spacelift fetches run logs in pages per run state and polls for more; it
  does not push them.
- Bulk actions run in the browser as one API call per item
  ([21 section 8](../reference/21-resources-scans-search.md#8-bulk-actions)).
- Multi-factor authentication uses WebAuthn security keys
  ([14 section 7.2](../reference/14-spaces-rbac-and-identity.md#72-mfa-and-security-keys)),
  a ceremony only browser code can perform.
- A Flows app may ship an optional `ui.js` browser bundle, uploaded with the
  app, alongside a declarative widget service (`ui.widget.set`)
  ([23 section 6.4](../reference/23-flows.md#64-bundle-format),
  [section 7.4](../reference/23-flows.md#74-services)). No public Spacelift
  app uses `ui.js`.

A survey of comparable Go-backed products, as of 2026-11-03:

| Product | Console stack |
|---|---|
| Argo CD | React 19 |
| Argo Workflows | React 18 |
| Coder | React 19 and Vite; Tailwind 4, Radix and shadcn-style components since it removed MUI and Emotion (PR 27821, August 2026) |
| Grafana | React 19; AngularJS removed in version 12 |
| Terrakube | React 19 with React Flow |
| Temporal UI | SvelteKit, served by Go through `embed.FS` |
| Woodpecker | Vue 3 |
| Gitea | Go templates and Vue; removed htmx in April 2026 (PR 37224) over CSP nonce problems, behaviour inherited invisibly between elements and attributes no linter checks; replaced Monaco with CodeMirror in March 2026 (PR 36764), cutting its JavaScript from 25 MB to 14 MB |
| Forgejo | Go templates, htmx 2 and Vue |
| Atlantis | Go templates, jQuery and xterm.js |
| OTF | templ, htmx 2.0.4, server-sent events and Alpine |

Products that ship a single-page app embed it in the Go binary through
`embed.FS`, and test it with Node tooling: Grafana, Gitea, Forgejo, Coder
and Temporal all use Playwright.

The console works within decisions already taken: the REST API with its
OpenAPI bundle is the only surface ([0001](0001-rest-first-spacelift-surface.md));
browser sessions are an opaque `HttpOnly` cookie
([0006](0006-authentication-and-tokens.md)); authorization is evaluated on
the server ([0007](0007-authorization-spaces-and-roles.md)); events reach
clients as server-sent event streams
([0015](0015-events-notifications-webhooks-audit.md)); infraweave ships one
binary with process modes ([0004](0004-repository-architecture.md),
[0020](0020-reference-deployments-and-packaging.md)); and installations may
be air-gapped.

## Decision

### Scope and position

- The console covers every surface of the inventory. It is one more client
  of the public REST API, beside the SDK, `infraweavectl` and the provider:
  it calls only operations of the served OpenAPI bundle, and no endpoint
  exists for the console alone. Bulk actions call the API once per item.
- Flows apps contribute declarative widgets only
  ([0018](0018-flows-runtime.md)). The console renders them with its own
  components; no app code runs in the browser.

### Stack

| Concern | Choice |
|---|---|
| Framework | React 19, TypeScript in strict mode, built with Vite |
| Routing | TanStack Router: typed routes and typed search parameters, so filters, sorting and selected tabs live in the URL |
| Server state | TanStack Query; the server-sent event streams of 0015 update its cache, and polling remains the fallback when a stream is closed |
| API client | orval generates the TypeScript client and the TanStack Query hooks from the served OpenAPI bundle (`api/deploy.openapi.yaml` and its siblings) |
| Components | Radix primitives styled with Tailwind 4; components are copied into the repository and owned there (the shadcn pattern), not imported from a styled kit |
| Theme | Design tokens as CSS custom properties, with light and dark sets; the console follows the system preference until the user picks one |
| Canvas | React Flow (`@xyflow/react`) for the Flows canvas, the stack dependency graph and the spaces tree |
| Code editing | CodeMirror 6 for Rego, CEL expressions and text templates, YAML validated against JSON Schema, HCL, and side-by-side and unified diffs |
| Editor intelligence | Autocomplete, type-checking, diagnostics, Rego parsing and workbench simulation come from Go API endpoints ([0024](0024-expression-language.md)), shared with `infraweavectl` and the provider; the console holds no language implementation of its own |

### Serving

- The production build is static files. The `infraweave` binary embeds them
  through `embed.FS`, and the `serve` process mode answers them at `/`. No
  Node.js process runs in production.
- A `GET` for a path with no file returns `index.html`, so deep links work.
  Only console routes fall back: a path under a prefix that another handler
  owns (`/api/`, `/operator/`, `/flows/`, `/mcp`, `/.well-known/`, `/auth/`, `/oauth/`,
  `/admin-login`, `/worker/`, `/vcs-agent/`, `/webhooks/`, `/downloads/`,
  `/blobs/`, `/state/`, `/registry/` and the other protocol endpoints)
  answers that handler's `404`, never `index.html`. The one console route
  under such a prefix is `/flows/forms/`, decision 0018's Forms page, which
  falls back like any console route.
- Hashed asset files are served with a one-year immutable cache;
  `index.html` is served with `no-cache`, so a new release reaches every
  browser on its next navigation.
- The embedded directory always exists (it holds a committed marker file),
  so `go build` succeeds without Node.js. When it holds no `index.html`,
  `serve` answers `/` with a built-in placeholder page that says the
  console was not built. Release builds always build the console first and
  fail when the embedded directory has no `index.html`.

### Identity and authority

- The console authenticates with the browser session cookie of 0006 and
  never holds a token in JavaScript. It is served from the API's origin, so
  no CORS configuration exists, and the browser's `Origin` header on writes
  satisfies 0006's check.
- Every resource the API returns carries `allowedActions`, computed on the
  server by the code that enforces those actions: the caller's grants
  (0007) and, for a resource with a state machine such as a run, the
  transitions its current state allows. The console shows, hides or
  disables controls from that list alone.
- The console never decides a permission or a state transition. The server
  enforces every action, and a refused action is shown from its problem
  response ([0022](0022-api-errors-and-update-semantics.md)).
- **One account at a time.** The console works in the session's account
  (0006). Its account switcher lists `GET /auth/accounts` and switches
  through `POST /auth/session/account`; a member that no account admits
  sees the switcher empty and nothing else but its profile.
- **The organization level** appears only when the organization has a
  second account, or under organization settings, which organization admins
  and the break-glass admin reach; an organization with one account reads
  as a single account ([0007](0007-authorization-spaces-and-roles.md#tenancy)).
  Organization settings use the `Organization` and `SSO` tags only.
- **OAuth consent.** `/consent/{request ID}` is the console route of
  0006's consent page: it shows the client, its redirect host, the scopes
  and the account switcher, and approves or denies through
  `POST /auth/oauth/requests/{id}/approve` or `/deny`.

### Delivery constraints on the page

- **Offline.** Every script, style, font and image ships in the embedded
  build. The console loads nothing from a CDN or any other origin.
- **Content Security Policy.** `serve` sends a strict policy with every
  console response: scripts only from the console's own origin, no inline
  scripts and no `eval`; styles from the console's own origin plus a nonce
  that `serve` writes into `index.html` per response, for libraries that
  insert `<style>` elements; `connect-src 'self'`, `frame-ancestors 'none'`,
  `object-src 'none'` and `base-uri 'none'`.
- **Accessibility.** WCAG 2.2 level AA.
- **Language.** Every user-visible string comes from a message catalogue
  keyed by message ID. English is the only catalogue.

### Infra Assistant

The console carries the assistant's chat panel. It supplies the current
route and the IDs of the resources on screen as the assistant's page
context, applies the assistant's navigation as a route change, and shows
the confirmation a destructive tool call requires; the boundary is
[0019](0019-ai-provider-boundary.md#console-boundary)'s.

### Repository and toolchain

- The console's code lives in `web/console/`.
- Node.js is pinned to an exact release of the current LTS line; npm is the
  package manager, with `package-lock.json` committed and installs run with
  `npm ci`. Dependabot covers the `npm` ecosystem in `web/console/`, and
  `deps-refresh.yml` moves the Node.js pin.
- The generated client and hooks are committed. `make generate` regenerates
  them with the other generated code, and `verify-generate` fails when they
  differ from the bundle.
- Build output is not committed.

### Quality gate

Each check blocks merges, as the Go gate does:

| Check | Tool | Bar |
|---|---|---|
| Types | `tsc --strict` | no errors |
| Lint | ESLint with typescript-eslint's `strict` configuration | no findings |
| Unit and component tests | Vitest | coverage 95% in total and 90% per directory; the generated client is excluded, as `pkg/client` is from the Go gate |
| End-to-end journeys | Playwright | every journey passes against a real `infraweave all` |

Playwright starts the dependencies with testcontainers and runs the
`infraweave` binary built with the console embedded and with `-cover`, as
decision 0016's acceptance harness does, so the journeys add to the merged
Go coverage. golangci-lint remains the only Go linter.

## Rationale

- **One client among several.** Building the console only on the public
  API means anything a user does in it, `infraweavectl`, the provider or an
  integrator can do too, and the API's tests cover the console's backend.
  A console-only endpoint would be an untested second contract.
- **React.** The hardest surfaces are client-heavy: the Flows canvas with
  its configuration panel and live invocation sidebar, four code editors
  with server diagnostics, live log streams, user-arranged dashboards and
  WebAuthn. React Flow, an actively released MIT canvas library, is built
  for React, so the canvas needs React whatever else is chosen. React is also the most
  common choice among comparable products (Argo CD, Coder, Grafana,
  Terrakube), which keeps contributors and patterns close.
- **A static single-page app.** Embedding static files keeps one binary and
  one origin, works offline, and needs nothing from an operator beyond what
  0020 already runs. It rules out frameworks that need a Node.js server:
  Next.js, Remix and React Server Components.
- **TanStack Router and Query.** Typed search parameters make every list
  view's filters a shareable URL that the compiler checks. Query's cache is
  the one place server state lives, so an event from a 0015 stream updates
  every view showing that resource.
- **orval.** It generates the client and the Query hooks in one step from
  the bundle infraweave already serves, and it releases weekly under MIT.
  openapi-typescript generates types only and had not released since
  February 2026; `@hey-api/openapi-ts` is not yet at 1.0. Committing the
  output and checking it in `verify-generate` makes an API change that
  breaks the console fail the build that made it.
- **Owned components.** Radix supplies accessible behaviour (focus, keyboard,
  ARIA) and Tailwind the styling; owning the components keeps their markup
  and styles under infraweave's control instead of a styled kit's release
  cycle. Coder moved to the same arrangement in 2026.
- **CodeMirror 6.** It is modular, small and MIT, with language packages
  for YAML, JSON Schema validation, HCL, Rego and CEL, and it accepts
  diagnostics and completions from a server, so the language logic stays in
  Go and serves every client.
- **`allowedActions` from the enforcing code.** A console that derived
  permissions or transitions itself would drift from the server and show
  actions that fail or hide ones that work.
- **Offline, CSP and accessibility.** Self-hosted installations often have
  no internet access. A strict policy with no inline scripts removes the
  usual cross-site scripting paths into a page that holds an operator's
  session. AA is the conformance level accessibility law and procurement
  rules commonly reference, and 2.2 is the current W3C Recommendation.
- **A catalogue from the start.** Extracting strings later means touching
  every component; a catalogue costs little while the console is written.
- **The placeholder page.** Go contributors build and test the server
  without Node.js, while a release can never ship without the console.
- **Departure from Spacelift:** Flows apps cannot ship browser code, so no
  third-party script runs in the console's origin under its session.
- **Departure from Spacelift:** live views take server-sent events where
  Spacelift polls; polling still works.

### Rejected alternatives

- **Server-driven Go pages (gomponents or templ) with Datastar or htmx and
  TypeScript islands.** The hardest pages, such as the Flows canvas beside
  its configuration panel and invocation sidebar, would split across two
  paradigms that share state through the DOM, and the canvas needs React
  anyway. templ is pre-1.0 (v0.3.1100). Datastar's core is MIT, but its Pro
  licence forbids use in open-source repositories.
- **Go compiled to WebAssembly.** A go-app hello world is 14.8 MB raw and
  3.7 MB compressed, before any feature. Vecty has been archived since
  October 2022, and Vugu is experimental.
- **htmx.** htmx 4.0.0 (28 August 2026) is a breaking rewrite that moves
  server-sent events into an extension, while npm's `latest` stays on 2.x
  until 2027. Gitea removed htmx for its CSP nonce problems, invisible
  inherited behaviour and attributes no linter checks.
- **Monaco.** Its minified build is about 5.8 MB compressed, and it brings
  its own language workers where infraweave serves language intelligence
  from Go. Gitea replaced it with CodeMirror and cut its JavaScript from
  25 MB to 14 MB.
- **Next.js, Remix and React Server Components.** Each needs a Node.js
  server in production beside the Go binary.
- **Go browser drivers for the journeys.** chromedp is active, but go-rod
  has had no code commits since December 2024 and playwright-go lags
  Playwright. Playwright itself is what Grafana, Gitea, Forgejo, Coder and
  Temporal use.

## Constraints

- `make generate` needs Node.js at the pinned version, because orval runs
  from `web/console/`'s lockfile; `go build` and the Go tests do not.
- Several CodeMirror language packages are small community projects:
  `codemirror-lang-hcl` 0.2.0, `codemirror-lang-rego` 0.1.0 and
  `codemirror-cel` 0.1.1. infraweave may have to maintain a fork. CodeMirror
  development moved from GitHub to `code.haverbeke.berlin/codemirror` in
  April 2026; the npm packages are unchanged.
- `allowedActions` on every resource is an infraweave addition where the
  reference carries it on only some entities.
- The TypeScript gate is part of this repository's engineering conventions
  (CLAUDE.md); the weaveplatform template does not yet carry it.

## Verification

- `make verify-generate` fails when the committed console client differs
  from what orval generates from the bundle.
- A blocking console job runs `npm ci`, `tsc --strict`, ESLint, Vitest with
  its coverage thresholds and the production build; a blocking end-to-end
  job runs the Playwright journeys. Both are in the `needs` of the
  `Quality gate` job.
- Journeys follow decision 0016's three-scenario rule for each area: the
  success path, a `403` denial (the control is absent or disabled, and a
  forced call shows the problem), and a failure path. They cover login
  through the `@oidc` issuer, live run logs over a 0015 stream, a policy
  edited with server diagnostics, a flow edited on the canvas, and an
  assistant confirmation.
- Every journey fails on a CSP violation report, on a request to any other
  origin, and on an axe-core accessibility violation at level AA.
- Go unit tests cover the `index.html` fallback, the paths that never fall
  back (`/oauth/` among them), the cache headers, the CSP header and the
  placeholder page.

## References

- Reports: [06](../reference/06-spacectl-and-clients.md),
  [08](../reference/08-run-lifecycle.md),
  [09](../reference/09-policies.md),
  [14](../reference/14-spaces-rbac-and-identity.md),
  [17](../reference/17-blueprints-and-templates.md),
  [18](../reference/18-scheduling-drift-dependencies.md),
  [21](../reference/21-resources-scans-search.md),
  [22](../reference/22-intelligence.md) and
  [23](../reference/23-flows.md), for the surfaces of the inventory.
- Peers: [Argo CD](https://github.com/argoproj/argo-cd),
  [Argo Workflows](https://github.com/argoproj/argo-workflows),
  [Coder](https://github.com/coder/coder) and
  [PR 27821](https://github.com/coder/coder/pull/27821),
  [Grafana](https://github.com/grafana/grafana),
  [Terrakube](https://github.com/terrakube-io/terrakube),
  [Temporal UI](https://github.com/temporalio/ui),
  [Woodpecker](https://github.com/woodpecker-ci/woodpecker),
  [Gitea](https://github.com/go-gitea/gitea),
  [PR 37224](https://github.com/go-gitea/gitea/pull/37224) and
  [PR 36764](https://github.com/go-gitea/gitea/pull/36764),
  [Forgejo](https://codeberg.org/forgejo/forgejo),
  [Atlantis](https://github.com/runatlantis/atlantis),
  [OTF](https://github.com/leg100/otf).
- Chosen: [React releases](https://github.com/facebook/react/releases)
  (19.3.1, MIT), [Vite](https://github.com/vitejs/vite),
  [TanStack Router](https://github.com/TanStack/router),
  [TanStack Query](https://github.com/TanStack/query),
  [orval releases](https://github.com/orval-labs/orval/releases) (8.46.0,
  MIT), [Radix Primitives](https://github.com/radix-ui/primitives),
  [Tailwind CSS](https://github.com/tailwindlabs/tailwindcss),
  [shadcn/ui](https://github.com/shadcn-ui/ui),
  [React Flow](https://github.com/xyflow/xyflow) (`@xyflow/react` 12.12.1,
  MIT), [CodeMirror](https://codemirror.net/),
  [codemirror-json-schema](https://github.com/jsonnext/codemirror-json-schema),
  [typescript-eslint](https://typescript-eslint.io/),
  [Vitest](https://github.com/vitest-dev/vitest),
  [Playwright releases](https://github.com/microsoft/playwright/releases)
  (1.64.1, Apache-2.0),
  [Testcontainers for Node.js](https://github.com/testcontainers/testcontainers-node),
  [WCAG 2.2](https://www.w3.org/TR/WCAG22/),
  [Node.js releases](https://nodejs.org/en/about/previous-releases).
- Compared: [openapi-typescript](https://github.com/openapi-ts/openapi-typescript/releases),
  [@hey-api/openapi-ts](https://github.com/hey-api/openapi-ts),
  [Monaco Editor releases](https://github.com/microsoft/monaco-editor/releases)
  (0.57.0), [templ releases](https://github.com/a-h/templ/releases),
  [gomponents](https://github.com/maragudk/gomponents),
  [htmx releases](https://github.com/bigskysoftware/htmx/releases),
  [Datastar](https://github.com/starfederation/datastar),
  [go-app](https://github.com/maxence-charriere/go-app),
  [Vecty](https://github.com/hexops/vecty),
  [Vugu](https://github.com/vugu/vugu),
  [chromedp](https://github.com/chromedp/chromedp),
  [go-rod](https://github.com/go-rod/rod),
  [playwright-go](https://github.com/playwright-community/playwright-go).
- Related records: [0001](0001-rest-first-spacelift-surface.md),
  [0006](0006-authentication-and-tokens.md),
  [0007](0007-authorization-spaces-and-roles.md),
  [0015](0015-events-notifications-webhooks-audit.md),
  [0016](0016-testing-and-acceptance.md),
  [0018](0018-flows-runtime.md),
  [0019](0019-ai-provider-boundary.md),
  [0022](0022-api-errors-and-update-semantics.md); [0024](0024-expression-language.md).
