@postgres
Feature: VCS webhook protocol refusals
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
