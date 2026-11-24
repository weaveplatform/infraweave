@identity @postgres
Feature: Organization identity providers
  An organization holds any number of identity providers of kind OIDC, SAML
  and GITHUB_OAUTH, the first of each kind its default. Its organization
  admins list, create, change and delete them and choose the default per
  kind through the Organization tag. A provider is stored disabled until a
  first sign-in through it succeeds, and its secret is sealed and never
  answered. Deleting a provider deletes the identities that sign in through
  it and ends their sessions on every replica, and deleting a kind's
  default makes the oldest remaining provider of the kind its default.
  The member, an account admin, holds the organization role only where a
  scenario gives it.
  ref: decisions 0006 and 0021

  Background:
    Given two infraweave servers, A and B, on one database, with an operator key
    And the home account's member has a browser session, admitted to home alone

  @op:organizationIdentityProviderCreate @op:organizationIdentityProviders @case:success
  Scenario: An organization admin creates an OIDC, a SAML and a GitHub OAuth provider, each disabled and its kind's default
    Given the home account's member holds organization-admin
    When the browser sends "POST /api/v1/organization/identity-providers" with '{"kind":"OIDC","name":"okta","secret":"oidc-client-secret","settings":{"identityProviderHost":"https://acme.okta.com","clientId":"infraweave"}}' through A
    Then it answers 201
    And the answer is the identity provider "okta"
    And the answer's isDefault is 'true'
    And the answer's enabled is 'false'
    And the answer's settings hold "claimMapping" as '{"entries":[]}'
    When the browser sends "POST /api/v1/organization/identity-providers" with '{"kind":"SAML","name":"adfs","settings":{"idpMetadataURL":"https://adfs.acme.example/FederationMetadata.xml","claimMapping":{"entries":[{"name":"subject","value":"uid"}]}}}' through B
    Then it answers 201
    And the answer is the identity provider "adfs"
    And the answer's settings hold "nameIDFormat" as '"TRANSIENT"'
    When the browser sends "POST /api/v1/organization/identity-providers" with '{"kind":"GITHUB_OAUTH","name":"github","secret":"github-client-secret","settings":{"clientId":"Iv1.acme","organization":"acme-corp"}}' through A
    Then it answers 201
    And the answer is the identity provider "github"
    And the answer's settings hold "baseUrl" as '"https://github.com"'
    When the browser sends "GET /api/v1/organization/identity-providers" through B
    Then the answer lists the identity providers "okta:OIDC:default:disabled, adfs:SAML:default:disabled, github:GITHUB_OAUTH:default:disabled"
    And the identity provider "okta"'s secret is sealed, never "oidc-client-secret"
    And the identity provider "github"'s secret is sealed, never "github-client-secret"
    And no answer of the scenario names "oidc-client-secret"
    And no answer of the scenario names "github-client-secret"
    And the home organization's trail holds 3 "identity_provider.create" entries

  @op:organizationIdentityProviderSetDefault @op:organizationIdentityProviderUpdate @op:organizationIdentityProviderDelete @op:organizationIdentityProviders @case:success
  Scenario: A second provider becomes the default, deleting the default promotes the oldest, and deleting a provider ends its sessions on every replica
    Given the home account's member holds organization-admin
    When the browser sends "POST /api/v1/organization/identity-providers" with '{"kind":"OIDC","name":"okta","secret":"s1","settings":{"identityProviderHost":"https://acme.okta.com","clientId":"a"}}' through A
    Then the answer is the identity provider "okta"
    When the browser sends "POST /api/v1/organization/identity-providers" with '{"kind":"OIDC","name":"entra","secret":"s2","settings":{"identityProviderHost":"https://login.microsoftonline.com/acme/v2.0","clientId":"b"}}' through B
    Then the answer is the identity provider "entra"
    When the browser sends "POST /api/v1/organization/identity-providers" with '{"kind":"OIDC","name":"auth0","secret":"s3","settings":{"identityProviderHost":"https://acme.auth0.com","clientId":"c"}}' through A
    Then the answer is the identity provider "auth0"
    When the browser sends "POST /api/v1/organization/identity-providers/{entra}/default" through B
    Then it answers 200
    And the answer's isDefault is 'true'
    When the browser sends "GET /api/v1/organization/identity-providers" through A
    Then the answer lists the identity providers "okta:OIDC:-:disabled, entra:OIDC:default:disabled, auth0:OIDC:-:disabled"
    When the browser sends "PATCH /api/v1/organization/identity-providers/{entra}" with '{"name":"Entra ID","secret":"rotated","settings":{"clientId":"b","claimMapping":{"entries":[{"name":"subject","value":"oid"}]}}}' through A
    Then it answers 200
    And the answer's name is '"Entra ID"'
    And the answer's settings hold "claimMapping" as '{"entries":[{"name":"subject","value":"oid"}]}'
    When the browser sends "DELETE /api/v1/organization/identity-providers/{entra}" through B
    Then it answers 200
    When the browser sends "GET /api/v1/organization/identity-providers" through A
    Then the answer lists the identity providers "okta:OIDC:default:disabled, auth0:OIDC:-:disabled"
    Given the browser's session signed in through the identity provider "auth0" as "alice"
    Then the identity provider "auth0" holds 1 identity
    When the browser sends "GET /api/v1/organization/identity-providers" through B
    Then it answers 200
    When the browser sends "DELETE /api/v1/organization/identity-providers/{auth0}" through A
    Then it answers 200
    And the identity provider "auth0" holds 0 identities
    And the browser's session is refused through B within a second
    And the home organization's trail holds 1 "identity_provider.set_default" entry
    And the home organization's trail holds 1 "identity_provider.update" entry
    And the home organization's trail holds 2 "identity_provider.delete" entries
    And no answer of the scenario names "rotated"

  @op:organizationIdentityProviderCreate @op:organizationIdentityProviderUpdate @op:organizationIdentityProviderDelete @op:organizationIdentityProviderSetDefault @op:organizationIdentityProviders @case:failure
  Scenario: Invalid settings, an immutable setting and an unknown provider are refused, naming the field
    Given the home account's member holds organization-admin
    When the browser sends "POST /api/v1/organization/identity-providers" with '{"kind":"OIDC","name":"okta","secret":"s","settings":{"identityProviderHost":"http://acme.okta.com","clientId":"a"}}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "settings.identityProviderHost"
    When the browser sends "POST /api/v1/organization/identity-providers" with '{"kind":"SAML","name":"adfs","settings":{"idpMetadata":"<x/>","idpMetadataURL":"https://adfs.acme.example/md","claimMapping":{"entries":[{"name":"subject","value":"uid"}]}}}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "settings.idpMetadata"
    When the browser sends "POST /api/v1/organization/identity-providers" with '{"kind":"SAML","name":"adfs","settings":{"idpMetadataURL":"https://adfs.acme.example/md","nameIDFormat":"EMAIL_ADDRESS"}}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "settings.claimMapping"
    When the browser sends "POST /api/v1/organization/identity-providers" with '{"kind":"SAML","name":"adfs","settings":{"idpMetadataURL":"https://adfs.acme.example/md","nameIDFormat":"UNSPECIFIED","claimMapping":{"entries":[{"name":"subject","value":"uid"}]}}}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "settings.nameIDFormat"
    When the browser sends "POST /api/v1/organization/identity-providers" with '{"kind":"OIDC","name":"okta","secret":"s","settings":{"identityProviderHost":"https://acme.okta.com","clientId":"a","claimMapping":{"entries":[{"name":"teams","value":"groups"}]}}}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "settings.claimMapping.entries[0].name"
    When the browser sends "POST /api/v1/organization/identity-providers" with '{"kind":"OIDC","name":"okta","secret":"s","settings":{"identityProviderHost":"https://acme.okta.com","clientId":"a","colour":"red"}}' through B
    Then it answers a 400 problem "invalid-request"
    And the problem refuses the field "/settings/colour" as "unknown-field"
    When the browser sends "GET /api/v1/organization/identity-providers" through A
    Then the answer lists the identity providers ""
    When the browser sends "POST /api/v1/organization/identity-providers" with '{"kind":"OIDC","name":"okta","secret":"s","settings":{"identityProviderHost":"https://acme.okta.com","clientId":"a"}}' through B
    Then the answer is the identity provider "okta"
    When the browser sends "PATCH /api/v1/organization/identity-providers/{okta}" with '{"settings":{"clientId":"another"}}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "settings.clientId"
    When the browser sends "PATCH /api/v1/organization/identity-providers/{okta}" with '{"kind":"SAML"}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "kind"
    When the browser sends "PATCH /api/v1/organization/identity-providers/01KACME0000000000000000099" with '{"name":"x"}' through A
    Then it answers a 404 problem "not-found"
    When the browser sends "DELETE /api/v1/organization/identity-providers/01KACME0000000000000000099" through B
    Then it answers a 404 problem "not-found"
    When the browser sends "POST /api/v1/organization/identity-providers/not-an-id/default" through A
    Then it answers a 404 problem "not-found"
    And the home organization's trail holds 1 "identity_provider.create" entry

  @op:organizationIdentityProviders @op:organizationIdentityProviderCreate @op:organizationIdentityProviderUpdate @op:organizationIdentityProviderDelete @op:organizationIdentityProviderSetDefault @case:denial
  Scenario: An account admin without the organization role is refused every identity provider operation
    When the browser sends "GET /api/v1/organization/identity-providers" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"ORGANIZATION_ADMIN"'
    When the browser sends "POST /api/v1/organization/identity-providers" with '{"kind":"OIDC","name":"okta","secret":"s","settings":{"identityProviderHost":"https://acme.okta.com","clientId":"a"}}' through B
    Then it answers a 403 problem "forbidden"
    When the browser sends "PATCH /api/v1/organization/identity-providers/01KACME0000000000000000099" with '{"name":"x"}' through A
    Then it answers a 403 problem "forbidden"
    When the browser sends "DELETE /api/v1/organization/identity-providers/01KACME0000000000000000099" through B
    Then it answers a 403 problem "forbidden"
    When the browser sends "POST /api/v1/organization/identity-providers/01KACME0000000000000000099/default" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"ORGANIZATION_ADMIN"'

  @op:organizationIdentityProviderCreate @case:denial
  Scenario: A tier without saml-sso or oidc-sso refuses those kinds, and admits a GitHub OAuth provider
    Given the home account's member holds organization-admin
    And the operator key is exchanged through A for token "op"
    When token "op" creates the tier "Gold" with the feature "organization" through A
    And token "op" gives the home organization the tier "Gold" through A
    And token "op" turns billing on through A
    Then the browser's "POST /api/v1/organization/identity-providers" with '{"kind":"SAML","name":"adfs","settings":{"idpMetadataURL":"https://adfs.acme.example/md","nameIDFormat":"PERSISTENT"}}' through B eventually answers a 403 problem "feature-not-in-tier"
    And the problem names the feature "saml-sso"
    And the browser's "POST /api/v1/organization/identity-providers" with '{"kind":"OIDC","name":"okta","secret":"s","settings":{"identityProviderHost":"https://acme.okta.com","clientId":"a"}}' through A eventually answers a 403 problem "feature-not-in-tier"
    And the problem names the feature "oidc-sso"
    When the browser sends "POST /api/v1/organization/identity-providers" with '{"kind":"GITHUB_OAUTH","name":"github","secret":"s","settings":{"clientId":"Iv1.acme","organization":"acme-corp"}}' through B
    Then it answers 201
