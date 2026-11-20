@policy-push @policy-push-forks @postgres @objectstore @github @gitlab @bitbucket-cloud @bitbucket-dc @azure-devops
Feature: Fork push policy authorization
  Readable forks have their own native repository identities. Even a fork that
  shares the original commit needs an explicit allow_fork policy decision.
  These local fixtures do not prove a unique fork-tip worker checkout; POL-18
  retains the real-provider source and worker acceptance boundary.
  ref: decision 0009
  ref: decision 0012
  ref: decision 0016

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a certificate signing request for a P-256 key is kept as "csr"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"acme","certificateSigningRequest":"{csr}"}' through A
    And it answers 201
    And the answer's id names "pool"

  Scenario Outline: Every native fork is refused until its policy explicitly allows it
    Given a "<provider>" plan-comment stack "infra" using "terraform" with labels '[]'
    And the event request source becomes a readable fork sharing the seed commit
    And token "h" sends "POST /api/v1/policies" with '{"name":"Fork authorization","type":"GIT_PUSH","body":"package infraweave\npropose if input.pull_request.head.branch == \"feature\"\nflag contains \"pushed\"\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    When the event fixture sends request "denied" through A
    Then event delivery "denied" settles with 0 new runs
    And the stored event request "denied" retains distinct native fork identities
    When token "h" sends "PATCH /api/v1/policies/{policy}" with '{"body":"package infraweave\npropose if input.pull_request.head.branch == \"feature\"\nallow_fork := true\nflag contains \"pushed\"\n"}' through B
    Then it answers 200
    When token "h" sends "GET /api/v1/policies/{policy}" through A
    Then the policy version for "policy" is kept from its ETag
    When the event fixture sends request "allowed" through B
    Then event delivery "allowed" settles with 1 new runs
    And the stored event request "allowed" retains distinct native fork identities
    And the "PROPOSED" event run on stack "infra" for commit "seed" is kept as "run"
    When token "h" sends "GET /api/v1/runs/{run}" through A
    Then the answer carries initial push evidence for policy "policy"
    And stack "infra" has exactly 1 event runs for commit "seed"

    Examples:
      | provider        |
      | GitHub          |
      | GitLab          |
      | Bitbucket Cloud |
      | Bitbucket DC    |
      | Azure DevOps    |
