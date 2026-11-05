@postgres
Feature: Startup refusals
  Every process mode opens PostgreSQL and checks its schema before any
  listener binds, so a database this binary must not run on never sees a
  request (decisions 0004 and 0005). infraweave all refuses a schema a newer
  incompatible release migrated, and a PostgreSQL below the floor, exits
  1 and says why.

  Scenario: infraweave all refuses a schema a newer release migrated
    Given the scenario's database was last migrated by infraweave v9.0.0, compatible from version 99 only
    When infraweave all is started on the scenario's database
    Then it exits before it listens, saying "run release v9.0.0 or newer"

  Scenario: infraweave all refuses PostgreSQL below the floor
    Given a PostgreSQL of its own older than the floor
    When infraweave all is started on that PostgreSQL
    Then it exits before it listens, saying "infraweave needs PostgreSQL 18 or newer"
