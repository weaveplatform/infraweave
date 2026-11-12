@postgres
Feature: Break-glass sign-in and browser sessions
  An organization's break-glass admin signs in at /admin-login/<organization>
  with the login and password the operator set. The session
  is a browser session in the cookie __Host-infraweave_session, of actor
  kind break_glass: an organization admin in no account, which the
  organization's routes admit and every account route treats as no
  credential. A wrong login or password, and an organization with no
  credential, answer one 401, and every attempt on an organization is on
  its trail. A cookie authenticates a write only with the public URL's
  Origin, so another site cannot make a signed-in browser write.
  ref: decision 0006

  Background:
    Given two infraweave servers, A and B, on one database, with an operator key
    And the password "correct horse" hashed by infraweave auth hash-password
    When the operator key is exchanged through A for token "op"
    And token "op" creates the organization "initech" with the account "main" and the break-glass login "admin" through A

  @op:viewer @case:success
  Scenario: The break-glass admin signs in as an organization admin in no account
    When the browser signs in to "initech" as "admin" with the password "correct horse" through A
    Then the browser holds the session cookie, HttpOnly, Secure, SameSite=Lax and Path=/
    And the viewer through B in the browser is the organization admin "admin", with no token
    And "GET /api/v1/sessions" through B in the browser without an Origin answers a 401 problem "unauthenticated"
    And "GET /api/v1/id" through A in the browser without an Origin answers a 401 problem "unauthenticated"
    And the browser's account switcher through A lists no account

  @case:denial
  Scenario: A wrong login or password, or no credential, answers one 401
    Then signing in to "initech" as "admin" with the password "wrong horse" through A with the public URL's Origin answers a 401 problem "unauthenticated"
    And signing in to "initech" as "root" with the password "correct horse" through B with the public URL's Origin answers a 401 problem "unauthenticated"
    And signing in to "globex" as "admin" with the password "correct horse" through A with the public URL's Origin answers a 401 problem "unauthenticated"
    When token "op" removes the break-glass credential of "initech" through A
    Then signing in to "initech" as "admin" with the password "correct horse" through B with the public URL's Origin answers a 401 problem "unauthenticated"
    And the browser holds no session cookie

  @case:success
  Scenario: Every sign-in on an organization is on its trail
    When the browser signs in to "initech" as "admin" with the password "correct horse" through A
    Then signing in to "initech" as "root" with the password "wrong horse" through B with the public URL's Origin answers a 401 problem "unauthenticated"
    And the trail of "initech" holds the break-glass sign-ins "success admin, refused root"

  @op:logout @case:denial
  Scenario: A cookie writes only with the public URL's Origin
    Then signing in to "initech" as "admin" with the password "correct horse" through A without an Origin answers a 401 problem "unauthenticated"
    And signing in to "initech" as "admin" with the password "correct horse" through A with another site's Origin answers a 401 problem "unauthenticated"
    When the browser signs in to "initech" as "admin" with the password "correct horse" through A
    Then "POST /api/v1/auth/logout" through A in the browser without an Origin answers a 401 problem "unauthenticated"
    And "POST /api/v1/auth/logout" through B in the browser with another site's Origin answers a 401 problem "unauthenticated"
    And the viewer through B in the browser answers 200
    When the browser logs out through A
    Then the browser holds no session cookie
    And the browser's ended cookie answers 401 at the viewer through B within a second

