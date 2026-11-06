@postgres
Feature: Account reads
  A credential works in one account (decision 0007), so the Account reads
  take no account: id, name, type and defaultPrivateWorkerPoolRunnerImage
  each answer for the caller's account. type is ORG, the reference's
  GitHub account type for an organization's account, and the runner image
  is decision 0008's Terraform runner image until worker pools set an
  account's default.

  @op:id @op:name @op:type @op:defaultPrivateWorkerPoolRunnerImage @case:success
  Scenario: Each read answers the caller's account
    Given two infraweave servers, A and B, on one database
    When the home key is exchanged through A for token "h"
    And the sibling key is exchanged through B for token "s"
    And the stranger key is exchanged through A for token "x"
    Then the account reads through A with token "h" answer the home account, type "ORG" and runner image "ghcr.io/weaveplatform/infraweave-runner-terraform"
    And the account reads through A with token "s" answer the sibling account, type "ORG" and runner image "ghcr.io/weaveplatform/infraweave-runner-terraform"
    And the account reads through B with token "x" answer the stranger account, type "ORG" and runner image "ghcr.io/weaveplatform/infraweave-runner-terraform"

  @op:id @op:name @op:type @op:defaultPrivateWorkerPoolRunnerImage @case:denial
  Scenario: Each read needs a credential
    Given two infraweave servers, A and B, on one database
    Then "GET /api/v1/id" without a credential answers a 401 problem "unauthenticated"
    And "GET /api/v1/name" without a credential answers a 401 problem "unauthenticated"
    And "GET /api/v1/type" without a credential answers a 401 problem "unauthenticated"
    And "GET /api/v1/default-private-worker-pool-runner-image" without a credential answers a 401 problem "unauthenticated"

  @op:id @op:name @op:type @op:defaultPrivateWorkerPoolRunnerImage @case:failure
  Scenario: Each read answers 503 while PostgreSQL is down and it needs the database
    Given a running infraweave server on a PostgreSQL of its own
    When the home key is exchanged through A for token "seen"
    And the home key is exchanged through A for token "unseen"
    Then the viewer through A with token "seen" answers 200
    When its PostgreSQL stops
    Then "GET /api/v1/name" through A with token "seen" answers a 503 problem "service-unavailable"
    And "GET /api/v1/id" through A with token "unseen" answers a 503 problem "service-unavailable"
    And "GET /api/v1/type" through A with token "unseen" answers a 503 problem "service-unavailable"
    And "GET /api/v1/default-private-worker-pool-runner-image" through A with token "unseen" answers a 503 problem "service-unavailable"
