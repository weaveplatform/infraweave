Feature: A scenario that needs Docker

  @postgres
  Scenario: A database scenario
    Given the scenario's PostgreSQL database
    Then it answers "SELECT 1" with "1"
