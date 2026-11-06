@postgres
Feature: API tokens and sessions
  A secret API key exchanges at POST /auth/token for an API token, which
  stands for a session row whose ID is the token's jti (decision 0006).
  Every refused exchange answers one problem, so the answer does not say
  which part failed. An account's sessions can be listed and revoked, and
  a revocation through one replica reaches the others through
  infraweave_invalidate. A session of another account answers 404, whether
  it is in the caller's organization or another (decision 0007). The
  tenants and their keys are seeded through SQL: home, sibling (another
  account of home's organization) and stranger (an account of another
  organization). Each server's own client holds a token of the home key, so
  the home account starts with one session per server.

  Background:
    Given two infraweave servers, A and B, on one database

  @op:apiKeyUser @op:viewer @case:success
  Scenario: A secret API key exchanges for a token of its account
    When the home key is exchanged through A for token "t1"
    Then token "t1" carries the claims of an API token of the home key
    And the viewer through B with token "t1" is the home key

  @op:apiKeyUser @case:denial
  Scenario: Every refused exchange answers the same problem
    Then exchanging the home key with a wrong secret answers a 401 problem "unauthenticated"
    And exchanging an unknown key answers a 401 problem "unauthenticated"
    And exchanging the home key once it is disabled answers a 401 problem "unauthenticated"
    And exchanging the home key once it has expired answers a 401 problem "unauthenticated"
    And exchanging the home key once it is deleted answers a 401 problem "unauthenticated"
    And every refused exchange answered the same problem

  @op:viewer @op:sessions @op:sessionDeleteAll @op:logout @case:denial
  Scenario: The account routes refuse a request without a credential
    Then "GET /api/v1/viewer" without a credential answers a 401 problem "unauthenticated"
    And the response challenges for a bearer token
    And "GET /api/v1/sessions" without a credential answers a 401 problem "unauthenticated"
    And "DELETE /api/v1/sessions" without a credential answers a 401 problem "unauthenticated"
    And "POST /api/v1/auth/logout" without a credential answers a 401 problem "unauthenticated"

  @op:sessions @case:success
  Scenario: The account's sessions are listed, the caller's marked current
    Given the home account has a member's browser session
    When the home key is exchanged through A for token "t1"
    Then the sessions listed through B with token "t1" are 4, its own current, the member's browser session among them
    When the stranger key is exchanged through A for token "s1"
    Then the sessions listed through A with token "s1" are its own alone

  @op:sessionDelete @case:success
  Scenario: A session deleted through one replica fails on the other within a second
    When the home key is exchanged through A for token "t1"
    And the home key is exchanged through A for token "t2"
    Then the viewer through B with token "t2" answers 200
    When token "t2"'s session is deleted through A with token "t1"
    Then the viewer through B with token "t2" answers 401 within a second
    And the viewer through B with token "t1" answers 200

  @op:sessionDelete @case:denial
  Scenario: A session of another account answers 404
    When the home key is exchanged through A for token "t1"
    And the sibling key is exchanged through A for token "sib"
    And the stranger key is exchanged through A for token "str"
    Then deleting token "sib"'s session through A with token "t1" answers a 404 problem "not-found"
    And deleting token "str"'s session through B with token "t1" answers a 404 problem "not-found"
    And deleting token "t1"'s session through A with token "str" answers a 404 problem "not-found"
    And the viewer through B with token "sib" answers 200
    And the viewer through A with token "str" answers 200

  @op:sessionDeleteAll @case:success
  Scenario: Deleting all sessions keeps the caller's own
    When the home key is exchanged through A for token "t1"
    And the home key is exchanged through A for token "t2"
    Then the viewer through B with token "t2" answers 200
    When every session but token "t1"'s is deleted through A
    Then the viewer through B with token "t2" answers 401 within a second
    And the viewer through B with token "t1" answers 200
    And the sessions listed through B with token "t1" are its own alone

  @op:logout @case:success
  Scenario: Logout ends the caller's session on every replica
    When the home key is exchanged through A for token "t1"
    Then the viewer through B with token "t1" answers 200
    When token "t1" logs out through A
    Then the viewer through B with token "t1" answers 401 within a second
    And the viewer through A with token "t1" answers 401
