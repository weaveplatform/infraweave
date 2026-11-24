@postgres @oidc
Feature: The federation issuer's discovery document and JWKS
  A relying party, such as a cloud's STS, Vault or Kubernetes, registers
  the installation as one OIDC identity provider. GET
  /.well-known/openid-configuration answers the federation issuer's
  discovery document: its issuer, auth.federation.issuer or the public
  URL; its jwks_uri, auth.federation.jwks_uri or the issuer's
  /.well-known/jwks; id_token, public subjects, the published keys'
  algorithms and the federation token's claims. GET /.well-known/jwks
  answers the federation keys alone, next, active and retiring, sorted by
  kid, each with use sig and alg and no certificate or private member, so
  no trust policy can accept an API or run token. Both answer GET and
  HEAD with no credential, as cacheable JSON for an hour, whatever the
  organization's tier, each request a standard draw on the client's
  address. An issuer with a path serves both under that path too. The
  scenarios sign tokens with a signing key's private half, opened from
  signing_keys under the scenario's keyring as the servers open it: the
  federation token's own issuance is not served yet.
  ref: decision 0006
  ref: decision 0003

  @case:success
  Scenario: Both replicas answer the default issuer's documents, whose keys verify a federation key's token and refuse an api-token key's
    Given two infraweave servers, A and B, on one database
    When "GET /.well-known/openid-configuration" with no credential through A answers cacheable JSON
    Then the discovery document names the issuer "http://infraweave.test", the JWKS URI "http://infraweave.test/.well-known/jwks" and the algorithm "RS256"
    And "GET /.well-known/openid-configuration" with no credential through B answers cacheable JSON
    And the discovery document names the issuer "http://infraweave.test", the JWKS URI "http://infraweave.test/.well-known/jwks" and the algorithm "RS256"
    When "GET /.well-known/jwks" with no credential through B answers cacheable JSON
    Then the JWKS holds exactly the published federation keys and no api-token key
    And "GET /.well-known/jwks" with no credential through A answers cacheable JSON
    And the JWKS holds exactly the published federation keys and no api-token key
    And "HEAD /.well-known/jwks" with no credential through A answers cacheable JSON
    And "HEAD /.well-known/openid-configuration" with no credential through B answers cacheable JSON
    When go-oidc discovers the issuer "http://infraweave.test" through A
    Then a token signed by the active federation key verifies against the discovered keys
    And a token signed by the active api-token key fails against the discovered keys

  @case:success
  Scenario: A rotation publishes the next key on both replicas before it signs, and the retired key leaves the JWKS
    Given two infraweave servers, A and B, on one database
    And "GET /.well-known/jwks" with no credential through A answers cacheable JSON
    And the JWKS's one federation key is kept as "fed.one"
    When the active federation key is aged past its lifetime, and authn.keys.rotate publishes a next key, kept as "fed.two"
    Then the JWKS through A eventually holds the federation keys "fed.one, fed.two"
    And the JWKS through B eventually holds the federation keys "fed.one, fed.two"
    And the federation key "fed.two" is next
    And the federation key "fed.one" is active
    When the next federation key is aged past its publication, and authn.keys.rotate promotes it
    Then the federation key "fed.two" is active
    And the federation key "fed.one" is retiring
    And "GET /.well-known/jwks" with no credential through B answers cacheable JSON
    And the JWKS holds exactly the published federation keys and no api-token key
    When the retiring federation key's tokens have expired, and authn.keys.rotate retires it
    Then the JWKS through A eventually holds the federation keys "fed.two"
    And the JWKS through B eventually holds the federation keys "fed.two"

  @case:success
  Scenario: A custom issuer with a path serves both documents under its path, and its custom jwks_uri verbatim
    Given two infraweave servers, A and B, on one database, with the federation issuer "http://infraweave.test/iw-federation" and the JWKS URI "https://keys.example.net/infraweave/jwks.json"
    When "GET /iw-federation/.well-known/openid-configuration" with no credential through A answers cacheable JSON
    Then the discovery document names the issuer "http://infraweave.test/iw-federation", the JWKS URI "https://keys.example.net/infraweave/jwks.json" and the algorithm "RS256"
    And "GET /.well-known/openid-configuration" with no credential through B answers cacheable JSON
    And the discovery document names the issuer "http://infraweave.test/iw-federation", the JWKS URI "https://keys.example.net/infraweave/jwks.json" and the algorithm "RS256"
    And "GET /iw-federation/.well-known/jwks" with no credential through B answers cacheable JSON
    And the JWKS holds exactly the published federation keys and no api-token key
    And "HEAD /iw-federation/.well-known/jwks" with no credential through A answers cacheable JSON
    And go-oidc discovers the issuer "http://infraweave.test/iw-federation" through B

  @case:success
  Scenario: An issuer with a path is discovered through its path, and its keys verify a federation key's token
    Given two infraweave servers, A and B, on one database, with the federation issuer "http://infraweave.test/iw-federation/"
    When "GET /iw-federation/.well-known/openid-configuration" with no credential through B answers cacheable JSON
    Then the discovery document names the issuer "http://infraweave.test/iw-federation/", the JWKS URI "http://infraweave.test/iw-federation/.well-known/jwks" and the algorithm "RS256"
    When go-oidc discovers the issuer "http://infraweave.test/iw-federation/" through A
    Then a token signed by the active federation key verifies against the discovered keys
    And a token signed by the active api-token key fails against the discovered keys

  @case:success
  Scenario: The documents answer without the oidc-federation feature, which still gates the subject template
    Given two infraweave servers, A and B, on one database, with oidc-federation switched off
    And the home key is exchanged through A for token "admin"
    When "GET /.well-known/openid-configuration" with no credential through A answers cacheable JSON
    Then the discovery document names the issuer "http://infraweave.test", the JWKS URI "http://infraweave.test/.well-known/jwks" and the algorithm "RS256"
    And "GET /.well-known/jwks" with no credential through B answers cacheable JSON
    And the JWKS holds exactly the published federation keys and no api-token key
    And "POST /api/v1/account/set-oidc-subject-template" through B with token "admin" and '{"template":"stack|{callerId}"}' answers a 403 problem "feature-disabled"

  @case:denial
  Scenario: An excess of reads draws down the client's standard budget across both replicas
    Given two infraweave servers, A and B, on one database, with a standard budget of 6
    When the federation documents are read with no credential through A and B in turn until the standard budget of 6 is spent
    Then it answers a 429 problem "rate-limited"
    And the response carries Retry-After
