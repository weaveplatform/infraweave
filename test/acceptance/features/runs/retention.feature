@postgres
Feature: How long run logs are kept
  runLogRetentionDays answers how many days the run-logs store keeps a
  run's logs: objectstore.retention.run-logs in days, a part of a day
  counting as a whole one, and 60 unless the server sets it. Any principal
  of the account reads it, whatever its bindings; a session no account
  admits, and a request with no credential, read nothing. The member key
  holds no binding.
  ref: decision 0008

  @op:runLogRetentionDays @case:success
  Scenario: Run logs are kept 60 days unless the server sets a retention
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    Then "GET /api/v1/run-log-retention-days" through A with token "h" answers '60'
    And "GET /api/v1/run-log-retention-days" through B with token "m" answers '60'

  @op:runLogRetentionDays @case:success
  Scenario: A server that keeps run logs 36 hours answers 2 days
    Given two infraweave servers, A and B, on one database, keeping run logs for 36h
    And the home key is exchanged through A for token "h"
    Then "GET /api/v1/run-log-retention-days" through B with token "h" answers '2'

  @op:runLogRetentionDays @case:denial
  Scenario: A member's session that no account admits reads no retention
    Given two infraweave servers, A and B, on one database
    And a member no account admits has a browser session
    When the browser sends "GET /api/v1/run-log-retention-days" through A
    Then it answers a 401 problem "unauthenticated"

  @op:runLogRetentionDays @case:failure
  Scenario: A read with no credential is refused
    Given two infraweave servers, A and B, on one database
    Then "GET /api/v1/run-log-retention-days" with no credential through B answers a 401 problem "unauthenticated"
