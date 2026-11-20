@policy-push-range @postgres @objectstore @github @gitlab @bitbucket-cloud @bitbucket-dc @azure-devops
Feature: Recovered push ranges remain bound to the signed native repository
  Recovery validates the original account integration and immutable repository
  identity before reading the head, history or files. Missing identity cannot
  silently fall back to mutable path lookup.
  ref: decision 0012
  ref: decision 0016

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"

  Scenario Outline: <provider> refuses a <identity> signed repository identity before crawling
    Given a "<provider>" plan-comment stack "infra" using "terraform" with labels '[]'
    And token "h" sends "POST /api/v1/policies" with '{"name":"No identity bypass","type":"GIT_PUSH","body":"package infraweave\ntrack := true\nlock := \"range\"\nflag contains \"pushed\"\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    And the event repository prepares a real native edit and revert on "main" as "collected"
    And the event push uses "missing" native commit metadata
    And the event push carries a "<identity>" signed repository identity
    And the event fixture holds future source preparations
    When the event fixture pushes "identity" from "seed" to "collected" on "main" with "valid" verification through B and receives 202
    And event delivery "identity" becomes due for one real preparation attempt
    Then event delivery "identity" refuses its repository identity before source reads or effects
    And stack "infra" has exactly 0 event runs for commit "collected"
    When token "h" sends "GET /api/v1/stacks/infra" through A
    Then the event stack is unlocked in its public read and owner storage

    Examples:
      | provider        | identity  |
      | GitHub          | different |
      | GitLab          | different |
      | Bitbucket Cloud | different |
      | Bitbucket DC    | different |
      | Azure DevOps    | different |
      | GitHub          | missing   |
      | GitLab          | missing   |
      | Bitbucket Cloud | missing   |
      | Bitbucket DC    | missing   |
      | Azure DevOps    | missing   |
