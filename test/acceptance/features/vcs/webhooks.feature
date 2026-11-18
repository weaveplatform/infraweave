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
    Then "GET /api/v1/vcs-event-triggered-runs-limit" through A with token "h" answers '2'

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
