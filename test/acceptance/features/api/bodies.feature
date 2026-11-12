@postgres
Feature: Every API surface bounds a request body
  A request body over server.max_body_bytes, 1 MiB by default, answers 413
  payload-too-large on the API, the operator API and the worker API before
  any control or handler buffers it, whatever credential the request
  carries. A body at the bound is read as any other: the strict body check
  refuses its one member, which no schema names. The home key is exchanged
  for token "h".
  ref: decision 0003

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"

  @op:stackCreate @case:failure
  Scenario: A body over the bound answers 413 on every surface, and one at the bound is read
    When token "h" sends "POST /api/v1/stacks" with a body of 1048577 bytes through A
    Then it answers a 413 problem "payload-too-large"
    When token "h" sends "POST /operator/v1/organizations" with a body of 1048577 bytes through B
    Then it answers a 413 problem "payload-too-large"
    When token "h" sends "POST /worker/v1/register" with a body of 1048577 bytes through A
    Then it answers a 413 problem "payload-too-large"
    When token "h" sends "POST /api/v1/stacks" with a body of 1048576 bytes through B
    Then it answers a 400 problem "invalid-request"
    And the problem refuses the field "/x" as "unknown-field"
