@postgres @vcs
Feature: A batch triggers a tracked run on each of several stacks at once
  A batch names stacks by slug, each optionally with a commit, and
  creates a TRACKED run on every one of them in one transaction, or on
  none. A named commit becomes the stack's tracked commit; a stack's run
  of the same commit that has not ended is answered in place of a new
  one. The route answers 403 to a caller who may trigger runs in no
  space. Every entry the rules refuse is refused together, each at its
  index: a stack the caller cannot read as one that does not exist, one
  in a space the caller only reads, one named twice, a disabled stack
  and a commit the stack's repository lacks. The home key is space-admin
  on the account's top space; the member key holds only the bindings a
  scenario gives it. Stacks infra and storage live in legacy; stack
  hidden lives in team.
  ref: decision 0008
  ref: decision 0016
  ref: decision 0022

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And a Gitea repository "infra" with the files:
      | main.tf | terraform {} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy"},"manageState":false}' through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Storage","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy"},"manageState":false}' through A
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Hidden","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"{team}"},"manageState":false}' through B
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And token "h" eventually reads the stack "storage" tracking the commit kept as "seed" through B

  @op:runTriggerBatch @case:success
  Scenario: A batch creates a tracked run on each stack, a named commit becomes its stack's tracked commit, and a run in flight is answered again
    Given a commit to "main" of "infra" changing "next.tf"
    And the head of "main" of "infra" is kept as "next"
    When token "h" sends "POST /api/v1/runs/batch" with '{"stacks":[{"stack":"storage","commitSha":"{next}"},{"stack":"infra"}],"forceApply":"SINGLE"}' through A
    Then it answers 200
    And the answer's batch runs name "stored, built"
    And token "h" eventually reads the run "stored" in the state "READY" through B
    When token "h" sends "GET /api/v1/stacks/storage" through A
    Then the answer tracks the commit kept as "next" at the branch head kept as "seed"
    When token "h" sends "GET /api/v1/runs/{stored}" through B
    Then it answers 200
    And the answer's type is '"TRACKED"'
    And the answer's commits is '["{next}"]'
    And the answer's forceApplyMode is '"SINGLE"'
    When token "h" sends "GET /api/v1/runs/{built}" through A
    Then it answers 200
    And the answer's type is '"TRACKED"'
    And the answer's commits is '["{seed}"]'
    When token "h" sends "POST /api/v1/runs/batch" with '{"stacks":[{"stack":"infra"}]}' through B
    Then it answers 200
    And the answer's runs is '[{"runId":"{built}","run":{"id":"{built}"}}]'
    When token "h" sends "GET /api/v1/stacks/infra/runs" through A
    Then the answer's items are the runs "built"

  @op:runTriggerBatch @case:denial
  Scenario: A caller who may trigger runs in no space is refused, and a batch naming a stack the caller cannot trigger runs on creates no run
    Given the member key is space-reader on the space "legacy"
    And the sibling key is exchanged through A for token "sib"
    When token "m" sends "POST /api/v1/runs/batch" with '{"stacks":[{"stack":"infra"}]}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"RUN_TRIGGER"'
    Given the member key is space-writer on the space "legacy"
    When token "m" sends "POST /api/v1/runs/batch" with '{"stacks":[{"stack":"infra"},{"stack":"hidden"}]}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/stacks/1/stack" as "not-found"
    Given the member key is space-reader on the space "{team}"
    When token "m" sends "POST /api/v1/runs/batch" with '{"stacks":[{"stack":"hidden"},{"stack":"infra"}]}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/stacks/0/stack" as "rule"
    When token "sib" sends "POST /api/v1/runs/batch" with '{"stacks":[{"stack":"infra"}]}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/stacks/0/stack" as "not-found"
    When token "h" sends "GET /api/v1/stacks/infra/runs" through A
    Then the answer's items are the runs ""
    When token "m" sends "POST /api/v1/runs/batch" with '{"stacks":[{"stack":"infra"}]}' through B
    Then it answers 200
    And the answer's batch runs name "mine"
    When token "h" sends "GET /api/v1/stacks/infra/runs" through A
    Then the answer's items are the runs "mine"

  @op:runTriggerBatch @case:failure
  Scenario: A stack named twice, an unknown stack, a commit the repository lacks, a disabled stack and an empty batch are refused, and nothing runs
    When token "h" sends "POST /api/v1/runs/batch" with '{"stacks":[{"stack":"infra"},{"stack":"infra"},{"stack":"nope"}]}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/stacks/1/stack" as "duplicate"
    And the problem refuses the field "/stacks/2/stack" as "not-found"
    When token "h" sends "POST /api/v1/stacks/storage/disable" through B
    Then it answers 200
    When token "h" sends "POST /api/v1/runs/batch" with '{"stacks":[{"stack":"infra","commitSha":"0123456789abcdef0123456789abcdef01234567"},{"stack":"storage"}]}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/stacks/0/commitSha" as "not-found"
    And the problem refuses the field "/stacks/1/stack" as "rule"
    When token "h" sends "POST /api/v1/runs/batch" with '{"stacks":[]}' through B
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "POST /api/v1/runs/batch" with '{"stacks":[{"stack":"infra","branch":"main"}]}' through A
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "GET /api/v1/stacks/infra/runs" through B
    Then the answer's items are the runs ""
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
