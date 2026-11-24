@postgres @vcs
Feature: A run's specification names the AWS roles its worker assumes
  An AWS integration that generates its credentials in the worker hands a
  run no credential: the clear part of the run's specification names the
  role the worker assumes, with the integration's custom external ID when
  it sets one or the external ID of the stack and the phase's side
  otherwise, the run's session name, the integration's duration and
  region, and the run, stack and space as session tags when the
  integration tags its sessions; neither part sets an AWS variable. The
  servers hold no AWS credentials; the home key is space-admin on the
  account's top space; stacks live in legacy and run on the pool.
  ref: decision 0006
  ref: decision 0008

  Background:
    Given the servers hold no AWS credentials
    And the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | terraform {} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "cert"
    And the launcher "l" registers with the key of "csr" and the certificate "cert" through A
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}"},"manageState":false}' through B

  @op:awsIntegrationAttach @op:stackProposedRuns @case:success
  Scenario: A worker's role reaches the specification with its custom external ID, and none of its credentials
    Given token "h" sends "POST /api/v1/aws-integrations" with '{"name":"Worker","space":"legacy","roleArn":"arn:aws:iam::222222222222:role/worker","region":"eu-west-1","generateCredentialsInWorker":true,"externalID":"ours","durationSeconds":7200,"tagAssumeRole":true,"labels":[]}' through A
    And the answer's id names "worker"
    And token "h" sends "PUT /api/v1/stacks/infra/aws-integrations/{worker}" with '{"read":true,"write":false}' through B
    When token "h" sends "POST /api/v1/stacks/infra/proposed-runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through B
    And token "l" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through A
    Then the answer assigns the run "run", its run token kept as "rt"
    When token "rt" sends "GET /worker/v1/runs/{run}/spec" through B
    Then it answers 200
    And the specification has its worker assume the AWS role "arn:aws:iam::222222222222:role/worker" of the integration "worker" named "Worker" with the external ID "ours", the session name "{run}@infra@acme", 7200 seconds, the region "eu-west-1" and the session tags "run={run},space=legacy,stack=infra", and sets no AWS variable
    And the specification's sealed part opens with the key of "csr" to no AWS credential

  @op:awsIntegrationAttach @op:runTrigger @case:success
  Scenario: A worker's role with no custom external ID reaches the specification with the external ID of the stack and the phase's side
    Given token "h" sends "POST /api/v1/aws-integrations" with '{"name":"Worker","space":"legacy","roleArn":"arn:aws:iam::222222222222:role/worker","region":"us-east-2","generateCredentialsInWorker":true,"labels":[]}' through A
    And the answer's id names "worker"
    And token "h" sends "PUT /api/v1/stacks/infra/aws-integrations/{worker}" with '{"read":true,"write":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through B
    And token "l" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through A
    Then the answer assigns the run "run", its run token kept as "rt"
    When token "rt" sends "GET /worker/v1/runs/{run}/spec" through B
    Then it answers 200
    And the specification has its worker assume the AWS role "arn:aws:iam::222222222222:role/worker" of the integration "worker" named "Worker" with the external ID "acme@{worker}@infra@read", the session name "{run}@infra@acme", 3600 seconds, the region "us-east-2" and the session tags "", and sets no AWS variable
    And the specification's sealed part opens with the key of "csr" to no AWS credential
