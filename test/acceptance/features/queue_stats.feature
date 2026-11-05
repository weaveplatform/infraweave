@postgres @op:messageQueueStats
Feature: Message queue depth
  An operator reads how many jobs each of decision 0011's seven logical
  queues holds: visible jobs are pending and due, in-flight jobs are
  running. The counts are installation-wide, as the reference serves them.

  Background:
    Given a running infraweave server

  @case:success
  Scenario: Every logical queue is reported with its ready and running jobs
    Given 2 due jobs, 1 running job and 1 job due tomorrow on the "events" queue
    And 1 running job on the "maintenance" queue
    Then messageQueueStats lists the seven logical queues in order, each served by "postgres"
    And messageQueueStats reports 2 visible and 1 in flight on the "events" queue
    And messageQueueStats reports 0 visible and 1 in flight on the "maintenance" queue
    And messageQueueStats reports 0 visible and 0 in flight on the "default" queue

  @case:success
  Scenario: The queues page by the requested limit
    Then messageQueueStats read 3 at a time lists the seven logical queues in order
