Feature: Scenarios a tag selection narrows

  @op:first
  Scenario: The first operation's
    Given anything

  @op:second
  Scenario: The second operation's
    Given anything

  @op:first @op:second
  Scenario: Both operations'
    Given anything

  Scenario: Neither operation's
    Given anything

  @vcs-lab @op:first
  Scenario: A lab's first operation's
    Given anything
