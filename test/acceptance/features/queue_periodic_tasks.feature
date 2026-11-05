@postgres
Feature: The work queue and periodic tasks across instances
  Every infraweave all instance runs the queue's workers and the scheduler
  ticker on one database (decision 0011). They share the work with no
  leader: a due periodic task fires once, a job that keeps failing dies
  with its last error, a rolled-back enqueue never runs, and a job whose
  instance dies is taken over once its lease lapses.

  Background:
    Given two infraweave all instances, A and B, on one database with a 2-second queue lease
    And each platform periodic task has run once across both instances

  Scenario: A due periodic task runs once across both instances
    When the "queue.prune" periodic task falls due
    Then "queue.prune" eventually runs once more, as one job
    And the instances logged "finished jobs pruned" once more between them

  Scenario: A job that fails every attempt dies with its last error
    When the idempotency_keys table is renamed away
    And the "idempotency.purge" periodic task falls due
    Then the newest "idempotency.purge" job eventually dies after 5 attempts with an error naming "idempotency_keys"

  Scenario: A job enqueued in a transaction that rolls back never runs
    When a "queue.prune" job with unique key "rolled-back" is enqueued in a transaction that rolls back
    And a "queue.prune" job with unique key "committed" is enqueued and committed
    Then the job with unique key "committed" eventually succeeds
    And no job with unique key "rolled-back" was ever stored
    And the instances logged "finished jobs pruned" once more between them

  Scenario: A killed instance's job is completed by the other within a lease
    Given a session holds an exclusive lock on idempotency_keys
    When the "idempotency.purge" periodic task falls due
    And the instance running "idempotency.purge" is killed
    And the lock is released
    Then the newest "idempotency.purge" job eventually succeeds on its attempt 2 after its lease lapsed
    And the surviving instance logged "expired idempotency keys purged"
