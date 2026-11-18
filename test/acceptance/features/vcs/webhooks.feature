@postgres
Feature: VCS webhook protocol, event run budgets and request history
  Webhook intake uses the shared request and address budgets, with its own
  25 MiB body bound and an empty protocol response on every path. Refused
  requests store no delivery, processing job or inbox object. These
  scenarios exercise the live route without a provider parser; verified
  delivery processing is covered by the PostgreSQL intake contract.
  Request snapshots retain every observed head before any run exists and
  stack reads resolve current and historical runs through the live API.
  ref: decision 0012
  ref: decision 0016
  ref: report 11

  Scenario: The webhook body bound applies before integration lookup
    Given two infraweave servers, A and B, on one database
    When a webhook body of 26214401 bytes is sent to "/webhooks/vcs/github_enterprise/01KACME000000000000000000Z" through A
    Then it answers 413
    And the webhook answer is empty with the shared headers
    When a webhook body of 26214400 bytes is sent to "/webhooks/vcs/github_enterprise/01KACME000000000000000000Z" through B
    Then it answers 404
    And the webhook answer is empty with the shared headers
    And no webhook delivery, processing job or inbox object is stored

  Scenario: Missing verification, deleted and mismatched integrations leave no stored work
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    When token "h" sends "POST /api/v1/gitlab-integrations" with '{"apiHost":"https://gitlab.example.com","privateToken":"unused-source-token","customInput":{"name":"Intake","spaceID":"acme"}}' through A
    Then it answers 201
    And the answer's id names "integration"
    When a webhook body of 2 bytes is sent to "/webhooks/vcs/gitlab/{integration}" through A
    Then it answers 401
    And the webhook answer is empty with the shared headers
    When a webhook body of 2 bytes is sent to "/webhooks/vcs/github_enterprise/{integration}" through B
    Then it answers 404
    And the webhook answer is empty with the shared headers
    When token "h" sends "DELETE /api/v1/gitlab-integrations/{integration}" through A
    Then it answers 200
    When a webhook body of 2 bytes is sent to "/webhooks/vcs/gitlab/{integration}" through B
    Then it answers 404
    And the webhook answer is empty with the shared headers
    And no webhook delivery, processing job or inbox object is stored

  Scenario: The expensive address budget is shared across replicas and precedes verification
    Given two infraweave servers, A and B, on one database whose expensive budget is 2 at 0.01 a second
    And a GITHUB_ENTERPRISE integration "Intake" in the space "acme" is kept as "integration"
    When a webhook body of 2 bytes is sent to "/webhooks/vcs/github_enterprise/01KACME000000000000000000Z" through A
    Then it answers 404
    And the webhook answer is empty with the shared headers
    When a webhook body of 2 bytes is sent to "/webhooks/vcs/github_enterprise/01KACME000000000000000000Z" through B
    Then it answers 404
    And the webhook answer is empty with the shared headers
    When a correctly signed GitHub webhook for integration "integration" is sent through A
    Then it answers 429
    And the webhook answer is empty with the shared headers
    And the response carries Retry-After and the rate-limit headers
    And its Retry-After is 100 seconds, give or take 10
    When a webhook body of 2 bytes is sent to "/webhooks/vcs/github_enterprise/{integration}" through B
    Then it answers 429
    And the webhook answer is empty with the shared headers
    And no webhook delivery, processing job or inbox object is stored

  Scenario: A database outage answers an empty 503 through either replica
    Given two infraweave servers, A and B, on a PostgreSQL of their own
    When a webhook body of 2 bytes is sent to "/webhooks/vcs/github_enterprise/01KACME000000000000000000Z" through A
    Then it answers 404
    And the webhook database stops
    When a webhook body of 2 bytes is sent to "/webhooks/vcs/github_enterprise/01KACME000000000000000000Z" through A
    Then it answers 503
    And the webhook answer is empty with the shared headers
    When a webhook body of 2 bytes is sent to "/webhooks/vcs/github_enterprise/01KACME000000000000000000Z" through B
    Then it answers 503
    And the webhook answer is empty with the shared headers
    When the webhook database starts again
    Then no webhook delivery, processing job or inbox object is stored

  @vcs @op:vcsEventTriggeredRunsLimit @case:success
  Scenario: The optional event run cap is read by members and isolated between accounts
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And the sibling key is exchanged through A for token "s"
    Then "GET /api/v1/vcs-event-triggered-runs-limit" through B with token "m" answers 'null'
    When token "h" sends "POST /api/v1/account/update-vcs-event-triggered-runs-limit" with '{"limit":2}' through A
    Then the answer is '2'
    And "GET /api/v1/vcs-event-triggered-runs-limit" through B with token "m" answers '2'
    And "GET /api/v1/vcs-event-triggered-runs-limit" through A with token "s" answers 'null'

  @vcs @op:accountUpdateVCSEventTriggeredRunsLimit @case:success
  Scenario: An admin sets the operator boundary, replays once and clears the event run cap
    Given two infraweave servers, A and B, whose VCS event run ceiling is 3
    And the home key is exchanged through A for token "h"
    When token "h" sets the VCS event run limit to '3' with key "cap-update" through A
    Then the answer is '3'
    When token "h" sets the VCS event run limit to '3' with key "cap-update" through B
    Then the answer is '3'
    And the VCS event run limit answer is replayed
    And the home account has 1 caller-attributed VCS event run limit audit entries
    And "GET /api/v1/vcs-event-triggered-runs-limit" through A with token "h" answers '3'
    When token "h" sends "POST /api/v1/account/update-vcs-event-triggered-runs-limit" with '{"limit":1}' through B
    Then the answer is '1'
    When token "h" sends "POST /api/v1/account/update-vcs-event-triggered-runs-limit" with '{"limit":null}' through A
    Then the answer is 'null'
    And "GET /api/v1/vcs-event-triggered-runs-limit" through B with token "h" answers 'null'
    And the home account has 3 caller-attributed VCS event run limit audit entries

  @gitlab @docker @terraform @objectstore @op:stack @case:success
  Scenario Outline: A stack lists request history and shared runs through its current integration
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And a GitLab user "dev" with a token kept as "tok"
    And a GitLab project "acme/platform/infra" that "dev" maintains, its head of "main" kept as "initial"
    And the GitLab project "acme/platform/infra" commits files on "main" as "seed":
      | root/main.tf | resource "terraform_data" "this" {\n  input = "base"\n}\n |
    And token "h" sends "POST /api/v1/gitlab-integrations" with '{"apiHost":"{gitlabHost}","privateToken":"{tok}","customInput":{"name":"Requests","spaceID":"acme","isDefault":<default>}}' through A
    And it answers 201
    And the answer's id names "integration"
    And the GitLab webhook secret is kept privately
    And a certificate signing request for a P-256 key is kept as "csr"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"acme","certificateSigningRequest":"{csr}"}' through A
    And it answers 201
    And the answer's id names "pool"
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","namespace":"acme/platform","repository":"infra","branch":"main","provider":"GITLAB","vcsIntegrationId":<binding>,"space":"acme","workerPool":"{pool}","projectRoot":"root","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And the GitLab project "acme/platform/infra" branches "feature" from "seed"
    And the GitLab project "acme/platform/infra" commits files on "feature" as "old":
      | root/main.tf | resource "terraform_data" "this" {\n  input = "old"\n}\n |
    When GitLab sends request snapshots for integration "integration" through A:
      | id | action | project             | head    | base | commit | draft |
      | 17 | open   | acme/platform/infra | feature | main | old    | true  |
    Then stack "infra" lists requests "" through B with token "h"
    When GitLab pushes "oldPush" for integration "integration" from "seed" to "old" on "feature" through B
    Then it answers 202
    And GitLab push "oldPush" for integration "integration" settles with 1 runs and 1 stack jobs
    And the "PROPOSED" event run on stack "infra" for commit "old" is kept as "oldRun"
    And token "h" eventually reads the run "oldRun" in the state "FINISHED" through A
    And the event run "oldRun" has a real tool plan through A
    And stack "infra" lists requests "17" through B with token "h"
    And request "17" on stack "infra" links runs "oldRun" through A
    When GitLab sends request snapshots for integration "integration" through B:
      | id | action | project             | head    | base | commit | draft |
      | 18 | open   | acme/platform/infra | feature | main | old    | false |
    Then stack "infra" lists requests "18, 17" through A with token "h"
    And request "18" on stack "infra" links runs "oldRun" through B
    When the GitLab project "acme/platform/infra" commits files on "feature" as "new":
      | root/main.tf | resource "terraform_data" "this" {\n  input = "new"\n}\n |
    And GitLab sends request snapshots for integration "integration" through A:
      | id | action | project             | head    | base | commit | draft |
      | 17 | update | acme/platform/infra | feature | main | new    | true  |
    Then stack "infra" lists requests "17, 18" through B with token "h"
    And request "17" on stack "infra" links runs "oldRun" through B
    When GitLab pushes "newPush" for integration "integration" from "old" to "new" on "feature" through A
    Then it answers 202
    And GitLab push "newPush" for integration "integration" settles with 1 runs and 1 stack jobs
    And the "PROPOSED" event run on stack "infra" for commit "new" is kept as "newRun"
    And token "h" eventually reads the run "newRun" in the state "FINISHED" through B
    And the event run "newRun" has a real tool plan through B
    And request "17" on stack "infra" links runs "newRun, oldRun" through A
    And request "18" on stack "infra" links runs "oldRun" through B
    And stack "infra" has the same requests through direct, list and search with token "h"
    When GitLab sends request snapshots for integration "integration" through B:
      | id | action | project             | head    | base | commit | draft |
      | 17 | close  | acme/platform/infra | feature | main | new    | true  |
    Then stack "infra" lists requests "18" through A with token "h"
    When GitLab sends request snapshots for integration "integration" through A:
      | id | action | project             | head    | base | commit | draft |
      | 17 | reopen | acme/platform/infra | feature | main | new    | true  |
    Then stack "infra" lists requests "17, 18" through B with token "h"
    When GitLab sends request snapshots for integration "integration" through B:
      | id | action | project             | head    | base | commit | draft |
      | 17 | merge  | acme/platform/infra | feature | main | new    | true  |
      | 18 | close  | acme/platform/infra | feature | main | old    | false |
    Then stack "infra" lists requests "" through A with token "h"
    And stack "infra" has exactly 1 event runs for commit "old"
    And stack "infra" has exactly 1 event runs for commit "new"
    And no GitLab webhook token reaches retained rows or server logs

    Examples:
      | default | binding         |
      | true    | null            |
      | false   | "{integration}" |

  @gitlab @terraform @op:stack @op:stacks @op:searchStacks @case:success @case:denial
  Scenario: Request scope and membership are applied before the fifty-request cap
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database whose expensive budget is 200 at 100 a second
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And the sibling key is exchanged through A for token "s"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And a GitLab user "dev" with a token kept as "tok"
    And a GitLab project "acme/platform/infra" that "dev" maintains, its head of "main" kept as "seed"
    And a GitLab project "acme/platform/other" that "dev" maintains, its head of "main" kept as "other"
    And token "h" sends "POST /api/v1/gitlab-integrations" with '{"apiHost":"{gitlabHost}","privateToken":"{tok}","customInput":{"name":"Requests","spaceID":"acme","isDefault":true}}' through A
    And it answers 201
    And the answer's id names "integration"
    And the GitLab webhook secret is kept privately
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","namespace":"acme/platform","repository":"infra","branch":"main","provider":"GITLAB","space":"legacy","vendorConfig":{"terraform":{}}},"manageState":false}' through B
    And it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And the GitLab project "acme/platform/infra" branches "feature" from "seed"
    And the GitLab project "acme/platform/infra" commits files on "feature" as "head":
      | main.tf | resource "terraform_data" "this" {} |
    When GitLab pushes "branchPush" for integration "integration" from "seed" to "head" on "feature" through B
    Then it answers 202
    And GitLab push "branchPush" for integration "integration" settles with 1 runs and 1 stack jobs
    And the "PROPOSED" event run on stack "infra" for commit "head" is kept as "headRun"
    When GitLab sends request snapshots for integration "integration" through A:
      | id | action | project             | head    | base    | commit | draft |
      | 1  | open   | acme/platform/infra | feature | main    | head   | true  |
      | 2  | open   | acme/platform/infra | feature | release | head   | false |
      | 3  | open   | acme/platform/other | feature | main    | head   | false |
      | 4  | open   | acme/platform/infra | feature | main    | seed   | false |
    Then stack "infra" lists requests "1" through B with token "h"
    And request "1" on stack "infra" links runs "headRun" through A
    When token "m" sends "GET /api/v1/stacks/infra" through A
    Then it answers a 404 problem "not-found"
    When token "s" sends "GET /api/v1/stacks/infra" through B
    Then it answers a 404 problem "not-found"
    Given the member key is space-reader on the space "legacy"
    Then stack "infra" lists requests "1" through A with token "m"
    And stack "infra" has the same requests through direct, list and search with token "m"
    When GitLab opens 60 requests on "acme/platform/infra" at commit "head" for integration "integration" through B
    And GitLab sends request snapshots for integration "integration" through A:
      | id  | action | project             | head    | base | commit | draft |
      | 999 | open   | acme/platform/infra | feature | main | seed   | false |
    Then stack "infra" lists the newest 50 of 60 eligible requests through B
    And stack "infra" has the same requests through direct, list and search with token "m"

  @op:vcsEventTriggeredRunsLimit @case:denial
  Scenario: Reading an event run cap requires authentication
    Given two infraweave servers, A and B, on one database
    Then "GET /api/v1/vcs-event-triggered-runs-limit" without a credential answers a 401 problem "unauthenticated"

  @op:accountUpdateVCSEventTriggeredRunsLimit @case:denial
  Scenario: A non-admin cannot set or clear the account event run cap
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    When token "h" sends "POST /api/v1/account/update-vcs-event-triggered-runs-limit" with '{"limit":2}' through A
    Then the answer is '2'
    When token "m" sends "POST /api/v1/account/update-vcs-event-triggered-runs-limit" with '{"limit":3}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"ACCOUNT_ADMIN"'
    And "GET /api/v1/vcs-event-triggered-runs-limit" through B with token "h" answers '2'
    When token "m" sends "POST /api/v1/account/update-vcs-event-triggered-runs-limit" with '{"limit":null}' through B
    Then it answers a 403 problem "forbidden"
    And "GET /api/v1/vcs-event-triggered-runs-limit" through A with token "h" answers '2'
    And "POST /api/v1/account/update-vcs-event-triggered-runs-limit" without a credential answers a 401 problem "unauthenticated"
    And "GET /api/v1/vcs-event-triggered-runs-limit" through B with token "h" answers '2'
    And the home account has 1 caller-attributed VCS event run limit audit entries

  @op:vcsEventTriggeredRunsLimit @case:failure
  Scenario: An unavailable database fails a cap read without losing its setting
    Given two infraweave servers, A and B, on a PostgreSQL of their own
    And the home key is exchanged through A for token "h"
    When token "h" sends "POST /api/v1/account/update-vcs-event-triggered-runs-limit" with '{"limit":2}' through A
    Then the answer is '2'
    When the webhook database stops
    And token "h" sends "GET /api/v1/vcs-event-triggered-runs-limit" through B
    Then it answers a 503 problem "service-unavailable"
    When the webhook database starts again
    Then "GET /api/v1/vcs-event-triggered-runs-limit" through A with token "h" eventually answers '2'

  @vcs @op:accountUpdateVCSEventTriggeredRunsLimit @case:failure
  Scenario: Both event run cap bounds name the configured ceiling and leave its value intact
    Given two infraweave servers, A and B, whose VCS event run ceiling is 3
    And the home key is exchanged through A for token "h"
    When token "h" sends "POST /api/v1/account/update-vcs-event-triggered-runs-limit" with '{"limit":2}' through A
    Then the answer is '2'
    When token "h" sends "POST /api/v1/account/update-vcs-event-triggered-runs-limit" with '{"limit":0}' through B
    Then it answers a 422 problem "validation-failed"
    And the VCS event run limit error has reason "minimum" and names ceiling 3 at /limit
    When token "h" sends "POST /api/v1/account/update-vcs-event-triggered-runs-limit" with '{"limit":4}' through A
    Then it answers a 422 problem "validation-failed"
    And the VCS event run limit error has reason "maximum" and names ceiling 3 at /limit
    And "GET /api/v1/vcs-event-triggered-runs-limit" through B with token "h" answers '2'
    And the home account has 1 caller-attributed VCS event run limit audit entries
