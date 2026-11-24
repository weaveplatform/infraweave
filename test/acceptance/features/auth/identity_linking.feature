@identity @oidc @postgres
Feature: Linking and unlinking a member's identities
  One person's identities at two identity providers are two members, and
  two seats, until an organization admin links them: nothing links two
  members by e-mail address. organizationMemberLinkCreate opens a link that
  merges the body's member into the path's for an hour and answers its
  page's URL. The person signs in on the page through a provider of each
  member, with a fresh authentication, the second sign-in from the browser
  that holds the first's session; a sign-in of a third identity, from
  another browser or after the hour is refused. The second sign-in merges
  the members in one transaction with its trail entry: the other member's
  identities, admissions, user bindings, personal keys and organization
  role move to the member, the other member's sessions end on every
  replica and its row and seat go, and the browser holds a session of the
  member. identityFederationUnlink removes the caller's member's identities
  of a provider kind but the one its session signed in through, never the
  member's last, and User.identity lists the member's identities.
  ref: decision 0006 § "Members and identity providers"

  Background:
    Given two infraweave servers, A and B, on one database, trusting the scenario's OIDC issuers, with an expensive budget of 40
    And the home organization has the OIDC provider "okta" at the in-process issuer, enabled with the admin login "root"
    And the home organization has the OIDC provider "entra" at the in-process issuer, enabled with the admin login "root"

  @op:organizationMemberLinkCreate @case:success
  Scenario: An organization admin links two members, the person signs in as each on either replica, and they merge into one seat
    Given the home account's member has a browser session, admitted to home alone
    And the home account's member holds organization-admin
    And the browser asks through A for a CLI token sealed to a new RSA key, as token "adm"
    And the home organization's live members are counted
    And the provider "okta" signs in "ada-sub" next with the claims '{"preferred_username":"ada","email":"ada@example.com","email_verified":true}'
    And the browser signs in through the provider "okta" at A
    And the provider "entra" signs in "ada-gh" next with the claims '{"preferred_username":"ada2","email":"ada@example.com","email_verified":true}'
    And the browser signs in through the provider "entra" at B
    Then the member "ada" is active with 1 identity
    And the member "ada2" is active with 1 identity
    And the home organization has 2 more live members than counted
    Given the member "ada" is kept as "ada"
    And the member "ada2" is kept as "ada2"
    When token "adm" sends "POST /api/v1/organization/members/{ada}/links" with '{"otherMemberId":"{ada2}"}' through A
    Then it answers 201
    And the answer's link is kept as "lk"
    And the answer's memberId is '"{ada}"'
    And the answer's otherMemberId is '"{ada2}"'
    And the home organization's trail holds 1 "member.link_create" entry
    Given the provider "entra" signs in "ada-gh" next with the claims '{"preferred_username":"ada2"}'
    When the browser signs in through the provider "entra" at B for the link "lk"
    Then the sign-in sends the browser back to the link "lk"
    And the home organization has 2 more live members than counted
    Given the provider "okta" signs in "ada-sub" next with the claims '{"preferred_username":"ada"}'
    When the browser signs in through the provider "okta" at A for the link "lk"
    Then the sign-in sends the browser back to the link "lk"
    And the browser's session is "ada"'s, signed in through "okta" with the teams ""
    And the browser's ended cookie answers 401 at the viewer through A within a second
    And the browser's ended cookie answers 401 at the viewer through B within a second
    And the member "ada" is active with 2 identities
    And the home organization has 1 more live member than counted
    And the home organization's trail holds 1 "member.merge" entry
    When the browser sends "GET /api/v1/viewer" through B
    Then the answer's identity is '{"id":"{ada}","federations":[{"provider":"OIDC","externalID":"ada-sub","externalUsername":"ada"},{"provider":"OIDC","externalID":"ada-gh","externalUsername":"ada2"}]}'
    Given the provider "entra" signs in "ada-gh" next with the claims '{"preferred_username":"ada2"}'
    When the browser signs in through the provider "entra" at A
    Then the browser's session is "ada2"'s, signed in through "entra" with the teams ""
    And the home organization has 1 more live member than counted
    When the browser sends "GET /api/v1/viewer" through B
    Then the answer's identity is '{"id":"{ada}","federations":[{"provider":"OIDC","externalID":"ada-sub","externalUsername":"ada"},{"provider":"OIDC","externalID":"ada-gh","externalUsername":"ada2"}]}'

  @op:organizationMemberLinkCreate @case:denial
  Scenario: A member without the organization role opens no link, and a link refuses a third identity and another browser
    Given the home account's member has a browser session, admitted to home alone
    And the browser asks through A for a CLI token sealed to a new RSA key, as token "acct"
    And the provider "okta" signs in "ada-sub" next with the claims '{"preferred_username":"ada"}'
    And the browser signs in through the provider "okta" at A
    And the provider "entra" signs in "ada-gh" next with the claims '{"preferred_username":"ada2"}'
    And the browser signs in through the provider "entra" at B
    And the provider "okta" signs in "carl-sub" next with the claims '{"preferred_username":"carl"}'
    And the browser signs in through the provider "okta" at A
    And the member "ada" is kept as "ada"
    And the member "ada2" is kept as "ada2"
    When token "acct" sends "POST /api/v1/organization/members/{ada}/links" with '{"otherMemberId":"{ada2}"}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"ORGANIZATION_ADMIN"'
    And the home organization's trail holds 0 "member.link_create" entries
    Given the home account's member holds organization-admin
    When token "acct" sends "POST /api/v1/organization/members/{ada}/links" with '{"otherMemberId":"{ada2}"}' through A
    Then it answers 201
    And the answer's link is kept as "lk"
    Given the provider "okta" signs in "carl-sub" next with the claims '{"preferred_username":"carl"}'
    When the browser signs in through the provider "okta" at B for the link "lk"
    Then the sign-in answers 403 with the code "not-linked"
    Given the provider "okta" signs in "eve-sub" next with the claims '{"preferred_username":"eve","email":"ada@example.com","email_verified":true}'
    When the browser signs in through the provider "okta" at A for the link "lk"
    Then the sign-in answers 403 with the code "not-linked"
    Given the provider "entra" signs in "ada-gh" next with the claims '{"preferred_username":"ada2"}'
    When another browser signs in through the provider "entra" at A for the link "lk"
    Then the sign-in sends the browser back to the link "lk"
    Given the provider "okta" signs in "ada-sub" next with the claims '{"preferred_username":"ada"}'
    When the browser signs in through the provider "okta" at B for the link "lk"
    Then the sign-in answers 403 with the code "other-browser"
    And the member "ada" is active with 1 identity
    And the member "ada2" is active with 1 identity
    And the home organization's trail holds 0 "member.merge" entries

  @op:organizationMemberLinkCreate @case:failure
  Scenario: A link of an unknown member, one member twice, a deactivated member or one without an identity is refused, as is a second open link, and an expired link proves nothing
    Given the home account's member has a browser session, admitted to home alone
    And the home account's member holds organization-admin
    And the browser asks through A for a CLI token sealed to a new RSA key, as token "adm"
    And the home organization's live members are counted
    And "dave" is a deactivated member of the home account
    And "erin" is a pending member of the home account
    And the provider "okta" signs in "ada-sub" next with the claims '{"preferred_username":"ada"}'
    And the browser signs in through the provider "okta" at A
    And the provider "entra" signs in "ada-gh" next with the claims '{"preferred_username":"ada2"}'
    And the browser signs in through the provider "entra" at B
    And the member "ada" is kept as "ada"
    And the member "ada2" is kept as "ada2"
    And the member "dave" also holds the identity "dave-sub" at the provider "okta" as "dave"
    And the member "dave" is kept as "dave"
    And the member "erin" is kept as "erin"
    When token "adm" sends "POST /api/v1/organization/members/01KZZZZZZZZZZZZZZZZZZZZZZZ/links" with '{"otherMemberId":"{ada2}"}' through A
    Then it answers a 404 problem "not-found"
    When token "adm" sends "POST /api/v1/organization/members/{ada}/links" with '{"otherMemberId":"not-a-member"}' through B
    Then it answers a 404 problem "not-found"
    When token "adm" sends "POST /api/v1/organization/members/{ada}/links" with '{"otherMemberId":"{ada}"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "otherMemberId"
    When token "adm" sends "POST /api/v1/organization/members/{dave}/links" with '{"otherMemberId":"{ada2}"}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "memberId"
    When token "adm" sends "POST /api/v1/organization/members/{ada}/links" with '{"otherMemberId":"{erin}"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "otherMemberId"
    When token "adm" sends "POST /api/v1/organization/members/{ada}/links" with '{"otherMemberId":"{ada2}","colour":"red"}' through B
    Then it answers a 400 problem "invalid-request"
    When token "adm" sends "POST /api/v1/organization/members/{ada}/links" with '{"otherMemberId":"{ada2}"}' through A
    Then it answers 201
    And the answer's link is kept as "lk"
    When token "adm" sends "POST /api/v1/organization/members/{ada2}/links" with '{"otherMemberId":"{ada}"}' through B
    Then it answers a 409 problem "already-exists"
    And the home organization's trail holds 1 "member.link_create" entry
    Given the link "lk" expires
    When the browser signs in through the provider "okta" at A for the link "lk"
    Then the sign-in answers 410 with the code "link-ended"
    And the home organization has 3 more live members than counted

  @op:identityFederationUnlink @case:success
  Scenario: A member unlinks the identities of a kind but the one it signed in through, and the viewer lists those left
    Given the provider "okta" signs in "ada-sub" next with the claims '{"preferred_username":"ada"}'
    And the browser signs in through the provider "okta" at A
    And the member "ada" is kept as "ada"
    And the member "ada" also holds the identity "ada-gh" at the provider "entra" as "ada2"
    When the browser sends "GET /api/v1/viewer" through B
    Then the answer's identity is '{"id":"{ada}","federations":[{"provider":"OIDC","externalID":"ada-sub","externalUsername":"ada"},{"provider":"OIDC","externalID":"ada-gh","externalUsername":"ada2"}]}'
    When the browser sends "DELETE /api/v1/identity-federations/OIDC" through B
    Then it answers 200
    And the answer's federations is '[{"provider":"OIDC","externalID":"ada-sub","externalUsername":"ada"}]'
    And the member "ada" is active with 1 identity
    And no live session is signed in through "entra"
    And the home organization's trail holds 1 "identity.unlink" entry
    And the viewer through A in the browser answers 200

  @op:identityFederationUnlink @case:denial
  Scenario: The break-glass admin holds no identity to unlink
    Given the password "correct horse" hashed by infraweave auth hash-password
    And the operator key is exchanged through A for token "op"
    And token "op" sets the break-glass login of "acme" to "admin" through A
    When the browser signs in to "acme" as "admin" with the password "correct horse" through B
    And the browser sends "DELETE /api/v1/identity-federations/OIDC" through A
    Then it answers a 403 problem "forbidden"
    And the home organization's trail holds 0 "identity.unlink" entries

  @op:identityFederationUnlink @case:failure
  Scenario: A member's last identity, a kind it holds none of and a provider that is no kind are refused
    Given the provider "okta" signs in "ada-sub" next with the claims '{"preferred_username":"ada"}'
    And the browser signs in through the provider "okta" at A
    When the browser sends "DELETE /api/v1/identity-federations/OIDC" through B
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "provider"
    When the browser sends "DELETE /api/v1/identity-federations/SAML" through A
    Then it answers a 404 problem "not-found"
    When the browser sends "DELETE /api/v1/identity-federations/GOOGLE_OAUTH" through B
    Then it answers a 404 problem "not-found"
    When the browser sends "DELETE /api/v1/identity-federations/nope" through A
    Then it answers a 422 problem "validation-failed"
    And the member "ada" is active with 1 identity
    And the home organization's trail holds 0 "identity.unlink" entries
