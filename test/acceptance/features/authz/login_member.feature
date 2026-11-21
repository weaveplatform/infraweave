@postgres @policy-login @policy-login-member
Feature: Authenticated identities enter only accounts whose LOGIN policies admit them
  The identity-session harness supplies original upstream facts. Account entry,
  account lists, policy execution and issued CLI tokens use real services.
  This does not claim a live organization IdP sign-in, which belongs to P23.
  ref: decisions 0006, 0007 and 0009; #658

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the sibling key is exchanged through B for token "s"

  @op:authAccounts @op:authSessionAccount @op:cliConfirmationToken @case:success @case:denial
  Scenario: A member enters A but not B and a derived CLI session reevaluates original identity facts
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Admit original identity","type":"LOGIN","body":"package infraweave\nallow if { input.session.member; input.session.teams == [\"ops\"] }\nroles[\"legacy\"][\"space-reader\"] if { true }\nteam contains \"rewritten\" if { true }\n"}' through A
    And it answers 201
    And token "s" sends "POST /api/v1/policies" with '{"name":"Other account guard","type":"LOGIN","body":"package infraweave\ndeny := true\ndeny_message contains \"OTHER_ACCOUNT_PRIVATE_REASON\" if { true }\n"}' through B
    And it answers 201
    And token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through A
    And it answers 200
    And token "s" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through B
    And it answers 200
    And the home account's member has a browser session, admitted to home and sibling
    And the browser awaits account entry with upstream membership "true" and original teams '["ops"]'
    Then the browser's account switcher through A lists "acme"
    And switching the browser to the sibling account through B answers a 404 problem "not-found"
    And the last refusal hides the other account's private policy message
    When the browser switches to the home account through A
    Then the browser asks through B for a CLI token sealed to a new RSA key, as token "cli"
    And token "cli"'s read of the space "legacy" through A allows exactly the role "space-reader"'s actions
    And token "cli"'s read of the space "acme" through B is not found

  @op:authAccounts @op:authSessionAccount @case:success @case:denial
  Scenario: A member admitted nowhere retains organization authentication and an empty switcher
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Home refuses","type":"LOGIN","body":"package infraweave\ndeny := true\n"}' through A
    And it answers 201
    And token "s" sends "POST /api/v1/policies" with '{"name":"Other refuses","type":"LOGIN","body":"package infraweave\ndeny := true\ndeny_message contains \"OTHER_ACCOUNT_PRIVATE_REASON\" if { true }\n"}' through B
    And it answers 201
    And token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through A
    And token "s" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through B
    And the home account's member has a browser session, admitted to home and sibling
    And the browser awaits account entry with upstream membership "true" and original teams '[]'
    Then the browser's account switcher through B lists no account
    And the viewer through A in the browser answers 200
    And switching the browser to the sibling account through A answers a 404 problem "not-found"
    And the last refusal hides the other account's private policy message

  @op:authAccounts @case:success @case:denial
  Scenario Outline: No-policy fallback uses original upstream membership rather than managed-user rows
    Given token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through A
    And token "s" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through B
    And the home account's member has a browser session, admitted to home and sibling
    And the browser awaits account entry with upstream membership "<member>" and original teams '[]'
    Then the browser's account switcher through A lists <accounts>

    Examples:
      | member  | accounts         |
      | true    | "acme, acme-dev" |
      | false   | no account       |
      | unknown | no account       |

  @vcs @policy-login-review @op:runReview @case:success
  Scenario: A LOGIN-admitted organization member without managed membership can review with their real identity
    Given a Gitea repository "infra" with the files:
      | main.tf | terraform {} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy"},"manageState":false}' through B
    And it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And token "h" sends "POST /api/v1/policies" with '{"name":"Unmanaged reviewer entry","type":"LOGIN","body":"package infraweave\nallow if { not input.session.member }\nroles[\"legacy\"][\"space-writer\"] if { true }\n"}' through A
    And it answers 201
    And token "h" sends "POST /api/v1/policies" with '{"name":"Actual reviewer identity","type":"APPROVAL","space":"legacy","body":"package infraweave\napprove if { input.reviews.current.approvals[0].identity.ulid == \"01KACME0000000000000000008\"; input.reviews.current.approvals[0].session.login == \"bob\" }\n"}' through B
    And it answers 201
    And the answer's id names "approval"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{approval}" through A
    And it answers 200
    And token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through B
    And it answers 200
    And a member no account admits has a browser session
    And the browser awaits account entry with upstream membership "false" and original teams '[]'
    And the browser switches to the home account through A
    And the browser asks through B for a CLI token sealed to a new RSA key, as token "cli"
    When token "cli" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    Then it answers 200
    And the answer's id names "run"
    When token "cli" sends "POST /api/v1/stacks/infra/runs/review" with '{"run":"{run}","decision":"APPROVE"}' through B
    Then it answers 200
    And token "cli" eventually reads the run "run" in the state "READY" through A
    And the answer's history is the states "QUEUED, READY"
