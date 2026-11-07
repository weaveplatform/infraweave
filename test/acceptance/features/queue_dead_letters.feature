@postgres
Feature: Dead jobs through the operator API
  A job that fails every attempt dies with its last error (decision 0011).
  The operator lists the dead jobs of every organization under
  /operator/v1/queue/jobs, retries one, which makes it pending again with
  its attempts reset, or deletes one, which cancels it for pruning; each
  answers the job as it was.

  Background:
    Given two infraweave all instances, A and B, on one database with a 2-second queue lease and an operator key
    And each platform periodic task has run once across both instances
    And the operator key is exchanged through A for token "op"
    When the idempotency_keys table is renamed away
    And the "idempotency.purge" periodic task falls due
    Then the newest "idempotency.purge" job eventually dies after 5 attempts with an error naming "idempotency_keys"

  @case:success
  Scenario: A retried dead job runs again
    Then the dead jobs listed through B with token "op" are the newest "idempotency.purge" job alone, naming "idempotency_keys"
    When the idempotency_keys table is renamed back
    And token "op" retries the dead job through A
    Then the retried job eventually succeeds
    And the dead jobs listed through B with token "op" are none

  @case:success @case:failure
  Scenario: A deleted dead job is no longer listed or read
    Then the dead jobs listed through A with token "op" are the newest "idempotency.purge" job alone, naming "idempotency_keys"
    When token "op" deletes the dead job through B
    Then the dead jobs listed through A with token "op" are none
    And reading the dead job through A with token "op" answers a 404 problem "not-found"
