@postgres @vcs
Feature: A stack's tracked commit
  A stack on a raw Git repository keeps its branch's head and the commit
  its runs use. Creating the stack, or changing its repository, branch or
  provider, sets both to the branch's head, set by infraweave. Syncing
  moves both to the head again; setting the current commit pins the
  tracked commit to any commit of the repository, set by the caller, and
  leaves the head where it is. A space writer syncs and sets; a space
  reader is refused, and a stack the caller cannot read answers as one
  that does not exist. A commit the repository lacks is refused at /sha,
  and a repository whose host does not answer is unavailable. The home key
  is space-admin on the account's top space; the member key holds only the
  bindings a scenario gives it. Stack infra lives in legacy and stack
  hidden in team. A raw Git repository's branches list by its URL.

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
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Hidden","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"{team}"},"manageState":false}' through A

  @op:stackCreate @op:stack @op:stackSyncCommit @op:stackSetCurrentCommit @case:success
  Scenario: A new stack tracks its branch's head, a sync follows a push, and a set pins an earlier commit
    Then token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And the answer's tracked commit was set by infraweave
    When a commit to "main" of "infra" changing "main.tf"
    And the head of "main" of "infra" is kept as "pushed"
    And token "h" sends "POST /api/v1/stacks/infra/sync-commit" through B
    Then it answers 200
    And the answer tracks the commit kept as "pushed" at the branch head kept as "pushed"
    And the answer's tracked commit was set by infraweave
    When token "h" sends "POST /api/v1/stacks/infra/set-current-commit" with '{"sha":"{seed}"}' through A
    Then it answers 200
    And the answer tracks the commit kept as "seed" at the branch head kept as "pushed"
    And the answer's tracked commit was not set by infraweave
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then the answer tracks the commit kept as "seed" at the branch head kept as "pushed"

  @op:stackSyncCommit @op:stackSetCurrentCommit @case:denial
  Scenario: A space reader is refused, and a stack the caller cannot read does not exist
    Given the member key is space-reader on the space "legacy"
    When token "m" sends "POST /api/v1/stacks/infra/sync-commit" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_SYNC_COMMIT"'
    When token "m" sends "POST /api/v1/stacks/infra/set-current-commit" with '{"sha":"{seed}"}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_SET_CURRENT_COMMIT"'
    When token "m" sends "POST /api/v1/stacks/hidden/sync-commit" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "POST /api/v1/stacks/hidden/set-current-commit" with '{"sha":"{seed}"}' through B
    Then it answers a 404 problem "not-found"
    Given the member key is space-writer on the space "legacy"
    When token "m" sends "POST /api/v1/stacks/infra/sync-commit" through A
    Then it answers 200

  @op:stackSyncCommit @op:stackSetCurrentCommit @case:failure
  Scenario: A commit the repository lacks is refused, and a host that does not answer is unavailable
    When token "h" sends "POST /api/v1/stacks/infra/set-current-commit" with '{"sha":"0123456789abcdef0123456789abcdef01234567"}' through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Gone","repository":"gone","branch":"main","provider":"GIT","repositoryURL":"https://127.0.0.1:1/gone.git","space":"legacy"},"manageState":false}' through B
    Then it answers 201
    When token "h" sends "POST /api/v1/stacks/gone/sync-commit" through A
    Then it answers a 503 problem "service-unavailable"
    When token "h" sends "POST /api/v1/stacks/gone/set-current-commit" with '{"sha":"{seed}"}' through B
    Then it answers a 503 problem "service-unavailable"

  @op:branches @case:success
  Scenario: A user pages a raw Git repository's branches by its URL
    When a commit to "dev" of "infra" changing "dev.tf"
    And token "h" sends "GET /api/v1/branches?provider=GIT&namespace=&repository=infra&repositoryURL={infra.url}&limit=1" through A
    Then it answers 200
    And the answer's items is '["dev"]'
    And the answer's nextCursor is kept as "next"
    When token "h" sends "GET /api/v1/branches?provider=GIT&namespace=&repository=infra&repositoryURL={infra.url}&limit=1&cursor={next}" through B
    Then it answers 200
    And the answer's items is '["main"]'
    And the answer leaves out "nextCursor"
