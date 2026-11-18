@postgres @gitlab
Feature: Verified GitLab webhook intake
  GitLab tokens verify before filtering. Consumed deliveries retain exact
  bytes and a body HMAC, and their jobs parse without a provider API call.
  Merge request notes remain policy data and start no run or comment command.

  # ref: decisions 0003, 0012, 0013 and 0016
  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a GitLab user "dev" with a token kept as "tok"
    And a GitLab project "acme/platform/infra" that "dev" maintains, its head of "main" kept as "seed"
    And token "h" sends "POST /api/v1/gitlab-integrations" with '{"apiHost":"{gitlabHost}","privateToken":"{tok}","customInput":{"name":"Webhook","spaceID":"acme","isDefault":true}}' through A
    And it answers 201
    And the answer's id names "integration"
    And the GitLab webhook secret is kept privately

  Scenario: Pushes, merge requests and merge request notes are verified and parsed
    When GitLab sends a "push" delivery "push" for integration "integration" with "current" token through A
    Then it answers 202
    And the webhook answer is empty with the shared headers
    And GitLab delivery "push" for integration "integration" retains exact bytes and the body HMAC
    And GitLab delivery "push" for integration "integration" is eventually dispatched
    When GitLab sends a "merge-request" delivery "mr" for integration "integration" with "current" token through B
    Then it answers 202
    And the webhook answer is empty with the shared headers
    And GitLab delivery "mr" for integration "integration" retains exact bytes and the body HMAC
    And GitLab delivery "mr" for integration "integration" is eventually dispatched
    When GitLab sends a "merge-request-note" delivery "note" for integration "integration" with "current" token through A
    Then it answers 202
    And the webhook answer is empty with the shared headers
    And GitLab delivery "note" for integration "integration" retains exact bytes and the body HMAC
    And GitLab delivery "note" for integration "integration" is eventually dispatched
    And GitLab integration "integration" retains exactly 3 deliveries
    And GitLab webhook processing starts no run or evaluation job and makes no provider request
    And no GitLab webhook token reaches retained rows or server logs

  Scenario: Explicit resend deduplicates while refused and filtered deliveries retain nothing
    When GitLab sends a "push" delivery "original" for integration "integration" with "current" token through A
    Then it answers 202
    And GitLab delivery "original" for integration "integration" is eventually dispatched
    When GitLab explicitly resends delivery "original" for integration "integration" through B
    Then it answers 200
    And the webhook answer is empty with the shared headers
    When GitLab sends a "pipeline" delivery "wrong" for integration "integration" with "wrong" token through A
    Then it answers 401
    And the webhook answer is empty with the shared headers
    When GitLab sends an "issue-note" delivery "missing" for integration "integration" with "missing" token through B
    Then it answers 401
    And the webhook answer is empty with the shared headers
    When GitLab sends an "issue-note" delivery "issue" for integration "integration" with "current" token through A
    Then it answers 200
    And the webhook answer is empty with the shared headers
    When GitLab sends a "pipeline" delivery "pipeline" for integration "integration" with "current" token through B
    Then it answers 200
    And the webhook answer is empty with the shared headers
    And GitLab integration "integration" retains exactly 1 delivery
    And GitLab made exactly 6 webhook attempts
    And no GitLab webhook token reaches retained rows or server logs

  Scenario: A malformed consumed payload is retained before its safe parse failure
    When GitLab sends a "malformed" delivery "malformed" for integration "integration" with "current" token through A
    Then it answers 202
    And the webhook answer is empty with the shared headers
    And GitLab delivery "malformed" for integration "integration" retains exact bytes and the body HMAC
    And GitLab delivery "malformed" for integration "integration" eventually records a safe parse failure
    And GitLab integration "integration" retains exactly 1 delivery
    And GitLab webhook processing starts no run or evaluation job and makes no provider request
    And no GitLab webhook token reaches retained rows or server logs
