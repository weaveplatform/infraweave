Feature: A scenario with a tag no partition lists

  @postgres @op:stacks @case:success
  Scenario: A listed scenario
    Given anything

  @redis
  Scenario: An unlisted scenario
    Given anything
