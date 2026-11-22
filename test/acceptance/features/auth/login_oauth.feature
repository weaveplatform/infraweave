@identity @postgres @policy-login @policy-login-oauth
Feature: OAuth consent and grants follow the account's LOGIN admission
  An active organization member needs no managed-user row under LOGIN_POLICIES.
  Consent records original authenticated facts, redemption reevaluates current
  policies, and refresh retains the grant's own permissions and identity facts.
  The local identity-session harness is not a live IdP sign-in capture.
  ref: decisions 0006, 0007 and 0009; #658

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" sends "POST /api/v1/policies" with '{"name":"OAuth entry","type":"LOGIN","body":"package infraweave\nallow if { not input.session.member; input.session.teams == [\"upstream\"] }\nroles[\"legacy\"][\"space-reader\"] if { true }\nteam contains \"effective\" if { true }\n"}' through A
    And it answers 201
    And the answer's id names "guard"
    And token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through B
    And it answers 200
    And a member no account admits has a browser session
    And the browser awaits account entry with upstream membership "false" and original teams '["upstream"]'
    And the client registers the redirect URI "http://127.0.0.1/callback" through B
    And the client asks through A to authorize "mcp:read" for "/mcp" with an S256 challenge, redirecting to "http://127.0.0.1/callback"

  @op:authOAuthRequestApprove @case:success
  Scenario: LOGIN admits an unmanaged organization member through consent, redemption and refresh
    When the browser approves the request for the home account through A
    Then the browser is sent to "http://127.0.0.1/callback" with a code and the client's state
    When the browser logs out through B
    When the client redeems the code through B
    Then it holds an access token for "/mcp" with the scope "mcp:read" for an hour, and a refresh token for member "bob"
    And the OAuth grant retains original LOGIN facts and its own account-scoped permissions
    When the client refreshes through A
    Then it holds a new refresh token and a new access token
    And it holds an access token for "/mcp" with the scope "mcp:read" for an hour, and a refresh token for member "bob"
    And the OAuth grant retains original LOGIN facts and its own account-scoped permissions

  @op:authOAuthRequestApprove @case:denial
  Scenario: A policy changed after consent refuses redemption without consuming the code
    When the browser approves the request for the home account through B
    Then the browser is sent to "http://127.0.0.1/callback" with a code and the client's state
    When token "h" sends "PUT /api/v1/policies/{guard}" with '{"name":"OAuth now refused","body":"package infraweave\ndeny := true\n"}' through A
    Then it answers 200
    When the client redeems the code through B
    Then the token endpoint answers "invalid_grant"
    And the OAuth refusal leaves the code unspent and no grant exists
    When token "h" sends "PUT /api/v1/policies/{guard}" with '{"name":"OAuth allowed again","body":"package infraweave\nallow if { not input.session.member; input.session.teams == [\"upstream\"] }\nroles[\"legacy\"][\"space-reader\"] if { true }\n"}' through B
    Then it answers 200
    When the client redeems the code through A
    Then it holds an access token for "/mcp" with the scope "mcp:read" for an hour, and a refresh token for member "bob"
