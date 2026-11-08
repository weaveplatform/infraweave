@postgres
Feature: Idempotency keys across replicas
  Two infraweave serve processes on one database share their idempotency
  keys: a key answers the same through either replica. Every operation
  answers 501 until a family implements it, and a 5xx releases its key, so
  these scenarios store a key's state in idempotency_keys themselves, as the
  replica that served the request would have. TestAccGuardedReplicas proves
  a response stored through one replica replays through the other until a
  family serves a POST. Each replica's client holds a token of its own from
  one API key, whose caller, api_key:<key ID>, owns the keys, so a key
  stored for one token answers the other.
  ref: decisions 0003, 0005 and 0006

  Background:
    Given two infraweave servers, A and B, on one database

  Scenario: A stored response replays through either replica
    Given a 201 response to "POST /api/v1/stacks" with body '{"name":"x"}' is stored under key "k1"
    Then "POST /api/v1/stacks" with body '{"name":"x"}' and key "k1" through A replays the stored 201
    And "POST /api/v1/stacks" with body '{"name":"x"}' and key "k1" through B replays the stored 201

  Scenario: A key in flight answers 409 on both replicas until its lock lapses
    Given "POST /api/v1/stacks" with body '{}' under key "k2" is in flight
    Then "POST /api/v1/stacks" with body '{}' and key "k2" through A answers a 409 problem "idempotency-key-in-flight"
    And "POST /api/v1/stacks" with body '{}' and key "k2" through B answers a 409 problem "idempotency-key-in-flight"
    When the lock of key "k2" lapses
    Then "POST /api/v1/stacks" with body '{}' and key "k2" through B answers a 501 problem "not-implemented"
    And key "k2" is no longer stored

  Scenario: A key reused with another request answers 422 on both replicas
    Given a 201 response to "POST /api/v1/stacks" with body '{"name":"x"}' is stored under key "k3"
    Then "POST /api/v1/stacks" with body '{"name":"y"}' and key "k3" through A answers a 422 problem "idempotency-key-reuse"
    And "POST /api/v1/stacks" with body '{"name":"y"}' and key "k3" through B answers a 422 problem "idempotency-key-reuse"
