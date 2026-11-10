@postgres @op:messageQueueStats
Feature: Message queue depth
  An operator reads how many jobs each of the work queue's seven logical
  queues holds: visible jobs are pending and due, in-flight jobs are
  running. The counts are installation-wide, as the reference serves them,
  so reading them needs ACCOUNT_ADMIN.
  ref: decision 0011

  Background:
    Given a running infraweave server

  @case:success
  Scenario: Every logical queue is reported with its ready and running jobs
    Given 2 due jobs, 1 running job and 1 job due tomorrow on the "events" queue
    And 1 running job on the "deliveries" queue
    Then messageQueueStats lists the seven logical queues in order, each served by "postgres"
    And messageQueueStats reports 2 visible and 1 in flight on the "events" queue
    And messageQueueStats reports 0 visible and 1 in flight on the "deliveries" queue
    And messageQueueStats reports 0 visible and 0 in flight on the "default" queue

  @case:success
  Scenario: The queues page by the requested limit
    Then messageQueueStats read 3 at a time lists the seven logical queues in order

  @case:denial
  Scenario: A key that writes in the top space but does not administer the account reads no queue
    Given the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" sends "POST /api/v1/api-key-role-bindings" with '{"apiKeyID":"01KACME000000000000000000A","roleID":"space-writer","spaceID":"acme"}' through A
    When token "m" sends "GET /api/v1/message-queue-stats" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"ACCOUNT_ADMIN"'

  @case:failure
  Scenario: A cursor no page of the queues issued is refused
    Given the home key is exchanged through A for token "h"
    When token "h" sends "GET /api/v1/message-queue-stats?cursor=nope" through A
    Then it answers a 400 problem "invalid-cursor"
