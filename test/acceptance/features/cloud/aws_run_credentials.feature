@postgres @objectstore @vcs @docker @tofu
Feature: Runs receive the AWS credentials the server assumes for them
  A run of a stack with an AWS integration attached receives the
  credentials of its role in AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY,
  AWS_SECURITY_TOKEN and AWS_SESSION_TOKEN, which the server assumes once
  for each container the run starts, from the side the phase reads: a
  proposed run plans with the read side; a tracked run plans with the read
  side and applies with the write side, or reads the write side from the
  start on a stack that deploys automatically; a task and a destroy run
  read the write side. A side no attachment serves hands the run no AWS
  variable. The server assumes the role with the side's external ID, the
  run's session name, the integration's duration and, when it tags its
  sessions, the run, stack and space as session tags. The run's log masks
  each value, and no log, artifact, saved workspace, answer or server log
  holds one. A stack an attachment serves lists the four variables in its
  runtimeConfig as computed write-only variables. A role that refuses the
  server, or AWS STS failing, fails the run in preparation, naming the
  integration; so does an integration whose worker assumes its role, on a
  launcher that cannot. Each hook prints the SHA-256 of the four
  variables, which the scenario compares with the credentials the fake AWS
  STS issued. The servers hold AWS credentials of account 111111111111,
  which the fake knows; the home key is space-admin on the account's top
  space; stacks live in legacy, keep their state in infraweave and run on
  the pool, whose launcher is real.
  ref: decision 0006
  ref: decision 0008
  ref: decision 0013

  Background:
    Given the servers hold AWS credentials of the account "111111111111"
    And the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the OpenTofu versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "this" {\n  input = "one"\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A

  @op:awsIntegrationAttach @op:runTrigger @op:runConfirm @op:runLogs @op:stack @case:success
  Scenario: A tracked run plans with the read side's credentials and applies with the write side's, which no log, object or answer holds
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"opentofu":{}},"afterPlan":["d() { printf %s \"$1\" | sha256sum | cut -c1-64; }; echo \"aws: $(d \"$AWS_ACCESS_KEY_ID\") $(d \"$AWS_SECRET_ACCESS_KEY\") $(d \"$AWS_SECURITY_TOKEN\") $(d \"$AWS_SESSION_TOKEN\")\"","echo \"secret: $AWS_SECRET_ACCESS_KEY\""],"afterApply":["d() { printf %s \"$1\" | sha256sum | cut -c1-64; }; echo \"aws: $(d \"$AWS_ACCESS_KEY_ID\") $(d \"$AWS_SECRET_ACCESS_KEY\") $(d \"$AWS_SECURITY_TOKEN\") $(d \"$AWS_SESSION_TOKEN\")\""]},"manageState":true}' through B
    And token "h" sends "POST /api/v1/aws-integrations" with '{"name":"Deploy","space":"legacy","roleArn":"arn:aws:iam::222222222222:role/deploy","generateCredentialsInWorker":false,"durationSeconds":900,"tagAssumeRole":true,"labels":[]}' through A
    And the answer's id names "deploy"
    And the AWS role "arn:aws:iam::222222222222:role/deploy" trusts the installation with the external ID "acme@{deploy}@infra@*"
    And token "h" sends "PUT /api/v1/stacks/infra/aws-integrations/{deploy}" with '{"read":true,"write":true}' through B
    When token "h" sends "GET /api/v1/stacks/infra" through A
    Then the answer's runtimeConfig lists the computed write-only variables "AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_SECURITY_TOKEN, AWS_SESSION_TOKEN"
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "UNCONFIRMED" through B
    And token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"run":"{run}"}' through A
    Then token "h" eventually reads the run "run" in the state "FINISHED" through B
    And AWS STS issued the run "run" credentials with the external IDs "acme@{deploy}@infra@read, acme@{deploy}@infra@write" alone
    And AWS STS issued the run "run" credentials of the role "arn:aws:iam::222222222222:role/deploy" with the external ID "acme@{deploy}@infra@read", the session name "{run}@infra@acme", 900 seconds and the session tags "run={run},space=legacy,stack=infra"
    And AWS STS issued the run "run" credentials of the role "arn:aws:iam::222222222222:role/deploy" with the external ID "acme@{deploy}@infra@write", the session name "{run}@infra@acme", 900 seconds and the session tags "run={run},space=legacy,stack=infra"
    And the digests of the credentials AWS STS issued the run "run" with the external ID "acme@{deploy}@infra@read" are kept as "read"
    And the digests of the credentials AWS STS issued the run "run" with the external ID "acme@{deploy}@infra@write" are kept as "write"
    And token "h" reads the run "run"'s PLANNING log through A, which holds "{read}\nsecret: *****" and not "{write}"
    And token "h" reads the run "run"'s APPLYING log through B, which holds "{write}" and not "{read}"
    And no credential AWS STS issued is in an answer, a server's log or any of at least 3 objects of the scenario's bucket

  @op:awsIntegrationAttach @op:stackProposedRuns @op:runTrigger @op:taskCreate @op:runDestroyTrigger @op:runLogs @case:success
  Scenario: A proposed run plans with the read side, and a run on a stack that deploys automatically, a task and a destroy run read the write side
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"opentofu":{}},"afterPlan":["echo \"aws variables: $(env | grep -c -e ^AWS_ACCESS_KEY_ID= -e ^AWS_SECRET_ACCESS_KEY= -e ^AWS_SECURITY_TOKEN= -e ^AWS_SESSION_TOKEN=)\"","d() { printf %s \"$1\" | sha256sum | cut -c1-64; }; echo \"aws: $(d \"$AWS_ACCESS_KEY_ID\") $(d \"$AWS_SECRET_ACCESS_KEY\") $(d \"$AWS_SECURITY_TOKEN\") $(d \"$AWS_SESSION_TOKEN\")\""],"beforePerform":["d() { printf %s \"$1\" | sha256sum | cut -c1-64; }; echo \"aws: $(d \"$AWS_ACCESS_KEY_ID\") $(d \"$AWS_SECRET_ACCESS_KEY\") $(d \"$AWS_SECURITY_TOKEN\") $(d \"$AWS_SESSION_TOKEN\")\""],"afterDestroy":["d() { printf %s \"$1\" | sha256sum | cut -c1-64; }; echo \"aws: $(d \"$AWS_ACCESS_KEY_ID\") $(d \"$AWS_SECRET_ACCESS_KEY\") $(d \"$AWS_SECURITY_TOKEN\") $(d \"$AWS_SESSION_TOKEN\")\""]},"manageState":true}' through B
    And token "h" sends "POST /api/v1/aws-integrations" with '{"name":"Deploy","space":"legacy","roleArn":"arn:aws:iam::222222222222:role/deploy","generateCredentialsInWorker":false,"labels":[]}' through A
    And the answer's id names "deploy"
    And the AWS role "arn:aws:iam::222222222222:role/deploy" trusts the installation with the external ID "acme@{deploy}@infra@*"
    And token "h" sends "PUT /api/v1/stacks/infra/aws-integrations/{deploy}" with '{"read":false,"write":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/proposed-runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "unread"
    Then token "h" eventually reads the run "unread" in the state "FINISHED" through B
    And AWS STS issued the run "unread" credentials with the external IDs "" alone
    And token "h" reads the run "unread"'s PLANNING log through A, which holds "aws variables: 0" and not "aws variables: 4"
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FINISHED" through B
    And AWS STS issued the run "run" credentials with the external IDs "acme@{deploy}@infra@write" alone
    And the digests of the credentials AWS STS issued the run "run" with the external ID "acme@{deploy}@infra@write" are kept as "deployed"
    And token "h" reads the run "run"'s PLANNING log through A, which holds "aws variables: 4\n{deployed}" and not "aws variables: 0"
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"echo done"}' through B
    And the answer's id names "task"
    Then token "h" eventually reads the run "task" in the state "FINISHED" through A
    And AWS STS issued the run "task" credentials with the external IDs "acme@{deploy}@infra@write" alone
    And the digests of the credentials AWS STS issued the run "task" with the external ID "acme@{deploy}@infra@write" are kept as "performed"
    And token "h" reads the run "task"'s PERFORMING log through B, which holds "{performed}" and not "{deployed}"
    When token "h" sends "POST /api/v1/aws-integrations" with '{"name":"Reader","space":"legacy","roleArn":"arn:aws:iam::222222222222:role/reader","generateCredentialsInWorker":false,"labels":[]}' through A
    And the answer's id names "reader"
    And the AWS role "arn:aws:iam::222222222222:role/reader" trusts the installation with the external ID "acme@{reader}@infra@read"
    And token "h" sends "PUT /api/v1/stacks/infra/aws-integrations/{reader}" with '{"read":true,"write":false}' through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/proposed-runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "proposed"
    Then token "h" eventually reads the run "proposed" in the state "FINISHED" through A
    And AWS STS issued the run "proposed" credentials with the external IDs "acme@{reader}@infra@read" alone
    And the digests of the credentials AWS STS issued the run "proposed" with the external ID "acme@{reader}@infra@read" are kept as "proposal"
    And token "h" reads the run "proposed"'s PLANNING log through B, which holds "aws variables: 4\n{proposal}" and not "{deployed}"
    When token "h" sends "POST /api/v1/stacks/infra/destroy-runs" through A
    And the answer's id names "destroy"
    Then token "h" eventually reads the run "destroy" in the state "FINISHED" through B
    And AWS STS issued the run "destroy" credentials with the external IDs "acme@{deploy}@infra@write" alone
    And the digests of the credentials AWS STS issued the run "destroy" with the external ID "acme@{deploy}@infra@write" are kept as "destroyed"
    And token "h" reads the run "destroy"'s DESTROYING log through A, which holds "{destroyed}" and not "{deployed}"

  @op:awsIntegrationAttach @op:runTrigger @op:run @case:failure
  Scenario: A role that refuses the server, AWS STS failing, or a worker's role on a launcher that cannot assume it fails the run in preparation, naming the integration
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"opentofu":{}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/aws-integrations" with '{"name":"Deploy","space":"legacy","roleArn":"arn:aws:iam::222222222222:role/deploy","generateCredentialsInWorker":false,"labels":[]}' through A
    And the answer's id names "deploy"
    And the AWS role "arn:aws:iam::222222222222:role/deploy" trusts the installation with the external ID "acme@{deploy}@infra@*"
    And token "h" sends "PUT /api/v1/stacks/infra/aws-integrations/{deploy}" with '{"read":true,"write":true}' through B
    And the AWS role "arn:aws:iam::222222222222:role/deploy" trusts the installation no longer
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "refused"
    Then token "h" eventually reads the run "refused" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, FAILED"
    And the answer's last state transition's note contains 'AWS integration Deploy could not assume role arn:aws:iam::222222222222:role/deploy: AccessDenied: you need to configure trust relationship'
    And AWS STS issued the run "refused" credentials with the external IDs "" alone
    Given AWS STS fails every call to assume the role "arn:aws:iam::222222222222:role/deploy"
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "failed"
    Then token "h" eventually reads the run "failed" in the state "FAILED" through A
    And the answer's last state transition's note contains 'AWS integration Deploy could not assume role arn:aws:iam::222222222222:role/deploy: AWS STS failed: '
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Other","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"opentofu":{}}},"manageState":true}' through A
    And token "h" sends "POST /api/v1/aws-integrations" with '{"name":"Worker","space":"legacy","roleArn":"arn:aws:iam::222222222222:role/worker","generateCredentialsInWorker":true,"labels":[]}' through B
    And the answer's id names "worker"
    And token "h" sends "PUT /api/v1/stacks/other/aws-integrations/{worker}" with '{"read":true,"write":true}' through A
    When token "h" sends "POST /api/v1/stacks/other/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "unassumed"
    Then token "h" eventually reads the run "unassumed" in the state "FAILED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, FAILED"
    And the answer's last state transition's note contains 'role on the worker: AWS integration Worker'
