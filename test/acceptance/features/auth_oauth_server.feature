@postgres
Feature: The OAuth server for MCP clients
  An MCP client authorizes through infraweave's built-in OAuth server:
  it registers itself (RFC 7591), sends the member to authorize with an
  S256 PKCE challenge for one MCP resource, and the console's consent page
  approves the request for one account the member is admitted to. The
  client redeems the code for an access token whose aud is the resource,
  and a refresh token that rotates on every use; presenting a spent one
  revokes the grant. The grant is a session of the member's, so removing
  the member from the account ends it.
  ref: decision 0006

  Background:
    Given two infraweave servers, A and B, on one database, with an operator key
    And the home key is exchanged through A for token "h"
    And the home account's member has a browser session, admitted to home alone
    And the home account's member is "alice"

  @op:authOAuthRequest @op:authOAuthRequestApprove @case:success
  Scenario: A client registers, the member consents, and the client redeems and refreshes
    When the client reads the authorization server metadata through A
    Then the metadata names the endpoints, the code flow with S256, public clients and the scopes "mcp:read mcp:write"
    When the client registers the redirect URI "http://127.0.0.1:33418/callback" through B
    And the client asks through A to authorize "mcp:write" for "/mcp" with an S256 challenge, redirecting to "http://127.0.0.1:41000/callback"
    Then it is sent to the console's consent page for the request
    When the browser reads the request through B
    Then the request names the redirect host "127.0.0.1" and the scopes "mcp:read, mcp:write"
    When the browser approves the request for the home account through A
    Then the browser is sent to "http://127.0.0.1:41000/callback" with a code and the client's state
    When the client redeems the code through B
    Then it holds an access token for "/mcp" with the scope "mcp:read mcp:write" for an hour, and a refresh token
    And the home account's sessions through A with token "h" list the grant
    When the client refreshes through A
    Then it holds a new refresh token and a new access token
    And it holds an access token for "/mcp" with the scope "mcp:read mcp:write" for an hour, and a refresh token

  @op:authOAuthRequest @op:authOAuthRequestApprove @op:authOAuthRequestDeny @case:denial @case:failure
  Scenario: The server refuses a plain challenge, a remote redirect outside the allowlist, and a replayed code or refresh token
    When the client registers the redirect URI "http://app.example.com/cb" through A
    Then the registration answers invalid_redirect_uri naming "http://app.example.com/cb"
    When the client registers the redirect URIs "https://apps.example.com/cb, http://localhost/cb" through A
    And the client asks through B to authorize "mcp:read" for "/flows/mcp" with a plain challenge, redirecting to "https://apps.example.com/cb"
    Then it is sent to "https://apps.example.com/cb" with the error "invalid_request"
    When the client asks through B to authorize "mcp:read" for "/flows/mcp" with an S256 challenge, redirecting to "https://apps.example.com/cb"
    Then it is sent to the console's consent page for the request
    When the browser approves the request for the sibling account through A
    Then it answers a 403 problem "forbidden"
    When the browser approves the request for the home account through A
    Then it answers a 403 problem "forbidden"
    When token "h" sends "POST /api/v1/account/set-o-auth-redirect-allowlist" with '{"hosts":["apps.example.com"]}' through B
    Then it answers 200
    When the browser approves the request for the home account through B
    Then the browser is sent to "https://apps.example.com/cb" with a code and the client's state
    When the browser approves the request for the home account through A
    Then it answers a 404 problem "not-found"
    When the client redeems the code through A
    Then it holds an access token for "/flows/mcp" with the scope "mcp:read" for an hour, and a refresh token
    When the client redeems the code again through B
    Then the token endpoint answers "invalid_grant"
    When the client refreshes through A
    Then the token endpoint answers "invalid_grant"
    When the client asks through A to authorize "mcp:read" for "/mcp" with an S256 challenge, redirecting to "http://localhost:5000/cb"
    And the browser approves the request for the home account through B
    And the client redeems the code through A
    And the client refreshes through B
    Then it holds a new refresh token and a new access token
    When the client refreshes with the spent refresh token through A
    Then the token endpoint answers "invalid_grant"
    When the client refreshes through B
    Then the token endpoint answers "invalid_grant"
    When token "h" sends "GET /api/v1/auth/oauth/requests/01KACME0000000000000000099" through A
    Then it answers a 403 problem "forbidden"
    When token "h" sends "POST /api/v1/auth/oauth/requests/01KACME0000000000000000099/deny" through B
    Then it answers a 403 problem "forbidden"

  @op:authOAuthRequestDeny @case:success @case:failure
  Scenario: A member denies a request, which sends the client access_denied
    When the client registers the redirect URI "http://localhost/cb" through A
    And the client asks through A to authorize "mcp:read" for "/flows/mcp/deploy" with an S256 challenge, redirecting to "http://localhost/cb"
    And the browser denies the request through B
    Then it is sent to "http://localhost/cb" with the error "access_denied"
    When the browser denies the request through A
    Then it answers a 404 problem "not-found"

  @op:managedUserDelete @case:success
  Scenario: Removing the member from the account ends their grant
    When the client registers the redirect URI "http://127.0.0.1/cb" through A
    And the client asks through A to authorize "mcp:read" for "/mcp" with an S256 challenge, redirecting to "http://127.0.0.1/cb"
    And the browser approves the request for the home account through B
    And the client redeems the code through A
    Then the home account's sessions through B with token "h" list the grant
    When token "h" sends "DELETE /api/v1/managed-users/{alice}" through A
    Then it answers 200
    When the client refreshes through B
    Then the token endpoint answers "invalid_grant"
    And the home account's sessions through A with token "h" leave out the grant
