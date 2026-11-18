@postgres @bitbucket-dc
Feature: Verified Bitbucket Data Center webhook intake
  The fake sends documented payloads with repository ID seven. Consumed
  deliveries retain their signatures and bytes, while signed diagnostics
  pings are acknowledged without stored work.

  # ref: decisions 0003, 0012, 0013 and 0016
  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a Bitbucket Data Center user "dev" with an access token kept as "tok"
    And token "h" sends "POST /api/v1/bitbucket-datacenter-integrations" with '{"apiHost":"{bitbucketDCHost}","userFacingHost":"{bitbucketDCHost}","accessToken":"{tok}","customInput":{"name":"DC hooks","spaceID":"acme","isDefault":true}}' through A
    And it answers 201
    And the answer's id names "integration"
    And the Bitbucket Data Center webhook secret is kept privately

  Scenario: Signed pushes, pull requests and comments are stored and parsed
    When Bitbucket sends a "push" delivery "push" for integration "integration" with "current" signature through A
    Then it answers 202
    And the webhook answer is empty with the shared headers
    And Bitbucket delivery "push" belongs to integration "integration" with its exact signed bytes and one processing job
    And Bitbucket delivery "push" is eventually dispatched
    When Bitbucket sends a "pull-request" delivery "pr" for integration "integration" with "current" signature through B
    Then it answers 202
    And the webhook answer is empty with the shared headers
    And Bitbucket delivery "pr" belongs to integration "integration" with its exact signed bytes and one processing job
    And Bitbucket delivery "pr" is eventually dispatched
    When Bitbucket sends a "pull-request-comment" delivery "comment" for integration "integration" with "current" signature through A
    Then it answers 202
    And the webhook answer is empty with the shared headers
    And Bitbucket delivery "comment" belongs to integration "integration" with its exact signed bytes and one processing job
    And Bitbucket delivery "comment" is eventually dispatched
    And Bitbucket integration "integration" retains exactly 3 deliveries
    And Bitbucket made exactly 3 webhook attempts
    And Bitbucket webhook processing starts no run or evaluation job and makes no provider request
    And no Bitbucket webhook secret or malformed payload reaches server logs

  Scenario: Redelivery deduplicates while refused, filtered and ping deliveries retain nothing
    When Bitbucket sends a "push" delivery "original" for integration "integration" with "current" signature through A
    Then it answers 202
    And Bitbucket delivery "original" is eventually dispatched
    When Bitbucket explicitly resends delivery "original" for integration "integration" through B
    Then it answers 200
    And the webhook answer is empty with the shared headers
    And Bitbucket delivery "original" belongs to integration "integration" with its exact signed bytes and one processing job
    When Bitbucket sends an "unsupported" delivery "wrong" for integration "integration" with "wrong" signature through A
    Then it answers 401
    And the webhook answer is empty with the shared headers
    When Bitbucket sends an "unsupported" delivery "unsigned" for integration "integration" with "missing" signature through B
    Then it answers 401
    And the webhook answer is empty with the shared headers
    When Bitbucket sends an "unsupported" delivery "filtered" for integration "integration" with "current" signature through A
    Then it answers 200
    And the webhook answer is empty with the shared headers
    When Bitbucket sends a "ping" delivery "ping" for integration "integration" with "current" signature through B
    Then it answers 200
    And the webhook answer is empty with the shared headers
    And Bitbucket integration "integration" retains exactly 1 delivery
    And Bitbucket made exactly 6 webhook attempts
    And no Bitbucket webhook secret or malformed payload reaches server logs

  Scenario: A malformed consumed payload is retained before a safe parse failure
    When Bitbucket sends a "malformed" delivery "malformed" for integration "integration" with "current" signature through B
    Then it answers 202
    And the webhook answer is empty with the shared headers
    And Bitbucket delivery "malformed" belongs to integration "integration" with its exact signed bytes and one processing job
    And Bitbucket delivery "malformed" eventually records a safe parse failure
    And Bitbucket integration "integration" retains exactly 1 delivery
    And Bitbucket webhook processing starts no run or evaluation job and makes no provider request
    And no Bitbucket webhook secret or malformed payload reaches server logs
