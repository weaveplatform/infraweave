@postgres
Feature: VCS webhook protocol and event run budgets
  Webhook intake uses the shared request and address budgets, with its own
  25 MiB body bound and an empty protocol response on every path. Refused
  requests store no delivery, processing job or inbox object. These
  scenarios exercise the live route without a provider parser; verified
  delivery processing is covered by the PostgreSQL intake contract.

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

  @gitlab @op:vcsIntegrationDeliveries @op:vcsIntegrationDeliveryReplay @case:success
  Scenario: An admin recovers failed GitLab processing and preserves both active and ended runs
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a GitLab user "dev" with a token kept as "tok"
    And a GitLab project "acme/platform/infra" that "dev" maintains, its head of "main" kept as "seed"
    And token "h" sends "POST /api/v1/gitlab-integrations" with '{"apiHost":"{gitlabHost}","privateToken":"{tok}","customInput":{"name":"Replay","spaceID":"acme","isDefault":true}}' through A
    And it answers 201
    And the answer's id names "integration"
    And the GitLab webhook secret is kept privately
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","namespace":"acme/platform","repository":"infra","branch":"main","provider":"GITLAB","space":"acme"},"manageState":false}' through B
    And it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And the GitLab project "acme/platform/infra" commits files on "main" as "head":
      | main.tf | resource "terraform_data" "this" {} |
    And GitLab source requests fail before event run creation
    When GitLab pushes "failedPush" for integration "integration" from "seed" to "head" on "main" through A
    Then it answers 202
    And delivery "failedPush" of integration "integration" is kept as "delivery"
    And delivery "delivery" exhausts processing before creating runs
    When token "h" lists integration "integration" deliveries with page size 1 after "" through B
    Then the delivery page contains only "delivery" in state "FAILED" and keeps cursor "failedCursor"
    Given GitLab source requests recover
    When token "h" replays delivery "delivery" of integration "integration" with key "recover-original" through A
    Then it answers 202
    And the answer's id is '"{delivery}"'
    When token "h" replays delivery "delivery" of integration "integration" with key "recover-original" through B
    Then it answers 202
    And the delivery replay answer is cached
    And delivery "delivery" generation 1 settles with 1 newly created runs
    And the only "TRACKED" event run for commit "head" is kept as "run"
    And stack "infra" has exactly 1 event runs for commit "head"
    And the home account has 1 caller-attributed delivery replay audits
    And token "h" eventually reads the run "run" in the state "READY" through B
    When token "h" replays delivery "delivery" of integration "integration" with key "dedup-active" through B
    Then it answers 202
    And delivery "delivery" generation 2 settles with 0 newly created runs
    And stack "infra" has exactly 1 event runs for commit "head"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/cancel" with '{}' through A
    Then it answers 200
    And the answer's canRetry is 'true'
    When token "h" replays delivery "delivery" of integration "integration" with key "dedup-ended" through A
    Then it answers 202
    And delivery "delivery" generation 3 settles with 0 newly created runs
    And stack "infra" has exactly 1 event runs for commit "head"
    And the home account has 3 caller-attributed delivery replay audits
    And no GitLab webhook token reaches retained rows or server logs

  @gitlab @op:vcsIntegrationDeliveries @case:success
  Scenario: Delivery search pages newest first and returns metadata only
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a GitLab user "dev" with a token kept as "tok"
    And a GitLab project "acme/platform/infra" that "dev" maintains, its head of "main" kept as "seed"
    And token "h" sends "POST /api/v1/gitlab-integrations" with '{"apiHost":"{gitlabHost}","privateToken":"{tok}","customInput":{"name":"Pages","spaceID":"acme"}}' through A
    And it answers 201
    And the answer's id names "integration"
    And the GitLab webhook secret is kept privately
    When GitLab sends a "merge-request" delivery "older" for integration "integration" with "current" token through A
    Then it answers 202
    And GitLab delivery "older" for integration "integration" is eventually dispatched
    And delivery "older" of integration "integration" is kept as "olderId"
    When GitLab sends a "merge-request-note" delivery "newer" for integration "integration" with "current" token through B
    Then it answers 202
    And GitLab delivery "newer" for integration "integration" is eventually dispatched
    And delivery "newer" of integration "integration" is kept as "newerId"
    When token "h" lists integration "integration" deliveries with page size 1 after "" through A
    Then the delivery page contains only "newerId" in state "DISPATCHED" and keeps cursor "nextPage"
    When token "h" lists integration "integration" deliveries with page size 1 after "nextPage" through B
    Then the delivery page contains only "olderId" in state "DISPATCHED" and keeps cursor "lastPage"
    And no GitLab webhook token reaches retained rows or server logs

  @gitlab @op:vcsIntegrationDeliveries @op:vcsIntegrationDeliveryReplay @case:denial
  Scenario: Delivery metadata and replay require the integration's admin action
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And a GitLab user "dev" with a token kept as "tok"
    And token "h" sends "POST /api/v1/gitlab-integrations" with '{"apiHost":"{gitlabHost}","privateToken":"{tok}","customInput":{"name":"Private","spaceID":"acme"}}' through A
    And it answers 201
    And the answer's id names "integration"
    When token "m" lists integration "integration" deliveries with page size 1 after "" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "POST /api/v1/vcs-integrations/{integration}/deliveries/01ARZ3NDEKTSV4RRFFQ69G5FAV/replay" through A
    Then it answers a 404 problem "not-found"
    Given the member key is space-reader on the space "acme"
    When token "m" lists integration "integration" deliveries with page size 1 after "" through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "POST /api/v1/vcs-integrations/{integration}/deliveries/01ARZ3NDEKTSV4RRFFQ69G5FAV/replay" through A
    Then it answers a 403 problem "forbidden"
    And the home account has 0 caller-attributed delivery replay audits

  @gitlab @op:vcsIntegrationDeliveries @case:failure
  Scenario: Delivery search refuses malformed cursors and missing integrations
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a GitLab user "dev" with a token kept as "tok"
    And token "h" sends "POST /api/v1/gitlab-integrations" with '{"apiHost":"{gitlabHost}","privateToken":"{tok}","customInput":{"name":"Pages","spaceID":"acme"}}' through A
    And it answers 201
    And the answer's id names "integration"
    When token "h" sends "GET /api/v1/vcs-integrations/{integration}/deliveries?input=%7B%22after%22%3A%22broken%22%7D" through B
    Then it answers a 400 problem "invalid-cursor"
    When token "h" sends "GET /api/v1/vcs-integrations/{integration}/deliveries?input=%7B%22unknown%22%3Atrue%7D" through A
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "GET /api/v1/vcs-integrations/01ARZ3NDEKTSV4RRFFQ69G5FAV/deliveries?input=%7B%7D" through B
    Then it answers a 404 problem "not-found"

  @gitlab @op:vcsIntegrationDeliveryReplay @case:failure
  Scenario: An elapsed inbox TTL refuses replay before the expiry sweep deletes the body
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a GitLab user "dev" with a token kept as "tok"
    And a GitLab project "acme/platform/infra" that "dev" maintains, its head of "main" kept as "seed"
    And token "h" sends "POST /api/v1/gitlab-integrations" with '{"apiHost":"{gitlabHost}","privateToken":"{tok}","customInput":{"name":"Expired","spaceID":"acme"}}' through A
    And it answers 201
    And the answer's id names "integration"
    And the GitLab webhook secret is kept privately
    When GitLab sends a large retained push "large" for integration "integration" through A
    Then it answers 202
    And delivery "large" of integration "integration" is kept as "delivery"
    And delivery "delivery" generation 0 settles with 0 newly created runs
    Given delivery "delivery" has an elapsed but unswept inbox body
    When token "h" replays delivery "delivery" of integration "integration" with key "expired" through B
    Then it answers a 409 problem "invalid-state"
    And delivery "delivery" keeps replay generation 0
    And the home account has 0 caller-attributed delivery replay audits

  @gitlab @op:vcsIntegrationDeliveryReplay @case:failure
  Scenario: A rotated-out signature and a missing delivery refuse replay
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a GitLab user "dev" with a token kept as "tok"
    And a GitLab project "acme/platform/infra" that "dev" maintains, its head of "main" kept as "seed"
    And token "h" sends "POST /api/v1/gitlab-integrations" with '{"apiHost":"{gitlabHost}","privateToken":"{tok}","customInput":{"name":"Rotated","spaceID":"acme"}}' through A
    And it answers 201
    And the answer's id names "integration"
    And the GitLab webhook secret is kept privately
    When GitLab sends a "merge-request" delivery "original" for integration "integration" with "current" token through A
    Then it answers 202
    And GitLab delivery "original" for integration "integration" is eventually dispatched
    And delivery "original" of integration "integration" is kept as "delivery"
    When token "h" sends "POST /api/v1/gitlab-integrations/{integration}/regenerate-webhook-secret" through A
    Then it answers 200
    When token "h" sends "POST /api/v1/gitlab-integrations/{integration}/regenerate-webhook-secret" through B
    Then it answers 200
    When token "h" replays delivery "delivery" of integration "integration" with key "rotated" through A
    Then it answers a 409 problem "invalid-state"
    And delivery "delivery" keeps replay generation 0
    When token "h" sends "POST /api/v1/vcs-integrations/{integration}/deliveries/01ARZ3NDEKTSV4RRFFQ69G5FAV/replay" through B
    Then it answers a 404 problem "not-found"
    And the home account has 0 caller-attributed delivery replay audits

  @gitlab @op:vcsIntegrationDeliveryReplay @case:failure
  Scenario: A genuine pending job prevents replay even before any worker claims it
    Given two infraweave servers, A and B, with inbox workers paused
    And the home key is exchanged through A for token "h"
    And a GitLab user "dev" with a token kept as "tok"
    And a GitLab project "acme/platform/infra" that "dev" maintains, its head of "main" kept as "seed"
    And token "h" sends "POST /api/v1/gitlab-integrations" with '{"apiHost":"{gitlabHost}","privateToken":"{tok}","customInput":{"name":"Pending","spaceID":"acme"}}' through A
    And it answers 201
    And the answer's id names "integration"
    And the GitLab webhook secret is kept privately
    When GitLab sends a "merge-request" delivery "pending" for integration "integration" with "current" token through A
    Then it answers 202
    And delivery "pending" of integration "integration" is kept as "delivery"
    When token "h" lists integration "integration" deliveries with page size 1 after "" through B
    Then the delivery page contains only "delivery" in state "RECEIVED" and keeps cursor "pendingCursor"
    When token "h" replays delivery "delivery" of integration "integration" with key "pending" through B
    Then it answers a 409 problem "invalid-state"
    And delivery "delivery" keeps replay generation 0
    And the home account has 0 caller-attributed delivery replay audits
