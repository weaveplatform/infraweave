@postgres @vault
Feature: Data key rotation and KEK migration
  Decision 0013 seals values in PostgreSQL, and the content keys of sealed
  stores' objects, under data keys the KEK wraps. infraweave secrets rotate
  makes a new data key while the servers serve, and secrets.reseal moves
  every value onto it and deletes the old key; infraweave secrets rewrap
  moves the data keys to another KEK without touching a value. No
  operation serves objects yet, so the scenarios write and read a sealed
  object as serve's blob store does, on the servers' database and object
  storage, with a keyring loaded at that moment.

  Background:
    Given two infraweave servers, A and B, on one database
    And a 201 response to "POST /api/v1/stacks" with body '{"name":"x"}' is stored under key "k1"
    And the object "acct/state-1" in the states store holds "terraform state"

  Scenario: A rotation while two servers serve leaves every value readable and deletes the old key
    When the operator runs infraweave secrets rotate
    Then "POST /api/v1/stacks" with body '{"name":"x"}' and key "k1" through A replays the stored 201
    And the object "acct/state-1" in the states store reads "terraform state"
    And data key 1 is eventually deleted
    And every sealed value is under data key 2
    And "POST /api/v1/stacks" with body '{"name":"x"}' and key "k1" through B replays the stored 201
    And "POST /api/v1/stacks" with body '{"name":"x"}' and key "k1" through A replays the stored 201
    And the object "acct/state-1" in the states store reads "terraform state"

  Scenario: A rewrap from the local KEK to Vault, after which the servers restart on Vault
    When the operator runs infraweave secrets rewrap to Vault
    Then data key 1 is wrapped by Vault
    When servers A and B restart with the Vault KEK
    Then "POST /api/v1/stacks" with body '{"name":"x"}' and key "k1" through A replays the stored 201
    And "POST /api/v1/stacks" with body '{"name":"x"}' and key "k1" through B replays the stored 201
    And the object "acct/state-1" in the states store reads "terraform state"
    And every sealed value is under data key 1
