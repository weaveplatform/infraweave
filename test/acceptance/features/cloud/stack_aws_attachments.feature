@postgres
Feature: A stack's AWS integration attachments
  A stack read and a page of stacks list each stack's AWS integration
  attachments: those stored, and those an integration's autoattach labels
  make for a label the stack carries, marked autoattached. Each names its
  integration, the sides it serves and the external ID its role is
  assumed with: the side's generated one, or a worker's integration's
  custom one. An attachment whose integration lives in a space the caller
  cannot read is left out; since a reader of a stack's space also reads
  every space the stack reaches, a caller sees the AWS integrations of
  each stack it may read. The servers hold AWS credentials of account
  111111111111, which a fake AWS STS knows; team inherits from acme, and
  stack infra, labelled prod, lives in team. Deploy, in team, is attached
  to infra for read, and Worker, in acme, attaches to every stack
  labelled prod for write.

  Background:
    Given the servers hold AWS credentials of the account "111111111111"
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"provider":"GIT","name":"Infra","repository":"infra","branch":"main","space":"{team}","labels":["prod"]},"manageState":false}' through A
    And token "h" sends "POST /api/v1/aws-integrations" with '{"name":"Deploy","space":"{team}","roleArn":"arn:aws:iam::222222222222:role/deploy","generateCredentialsInWorker":false,"labels":[]}' through B
    And the answer's id names "deploy"
    And the AWS role "arn:aws:iam::222222222222:role/deploy" trusts the installation with the external ID "acme@{deploy}@infra@*"
    And token "h" sends "PUT /api/v1/stacks/infra/aws-integrations/{deploy}" with '{"read":true,"write":false}' through A
    And token "h" sends "POST /api/v1/aws-integrations" with '{"name":"Worker","space":"acme","roleArn":"arn:aws:iam::222222222222:role/worker","generateCredentialsInWorker":true,"externalID":"ours","labels":["autoattach_write:prod"],"autoattachEnabled":true}' through B

  @op:stack @op:stacks @case:success
  Scenario: A stack lists its stored and its autoattached AWS integrations
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then it answers 200
    And the stack "infra" lists the AWS integrations "Deploy:r, Worker:w:auto"
    And the stack "infra" shows the AWS integration "Deploy" with the external ID "acme@{deploy}@infra@read"
    And the stack "infra" shows the AWS integration "Worker" with the external ID "ours"
    When token "h" sends "GET /api/v1/stacks" through A
    Then it answers 200
    And the stack "infra" lists the AWS integrations "Deploy:r, Worker:w:auto"

  @op:stack @op:stacks @case:denial
  Scenario: A caller who cannot read a stack's space sees none of its AWS integrations
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"provider":"GIT","name":"Web","repository":"web","branch":"main","space":"legacy"},"manageState":false}' through B
    And the member key is space-reader on the space "legacy"
    When token "m" sends "GET /api/v1/stacks/infra" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/stacks" through B
    Then the answer lists the stacks "web"
    And the stack "web" lists the AWS integrations ""
