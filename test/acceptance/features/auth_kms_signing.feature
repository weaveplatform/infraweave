@postgres @vault
Feature: Tokens signed through a KMS
  With auth.signing.backend kms, the api-token signing keys live in a
  KMS the operator names in auth.signing.kms_keys, and infraweave signs
  through it: the private half never enters the process or the database.
  signing_keys holds the key's reference in place of a sealed private
  half. Each server opens every reference at start and refuses to start
  with a key that cannot sign ES256, naming it. The scenarios sign with a
  version of a Transit key on the @vault container, which the servers
  reach through secrets.kek.vault's address and token while the KEK stays
  local.
  ref: decision 0006

  Scenario: A token signed through Transit by one replica verifies on the other
    Given two infraweave servers, A and B, on one database, signing with an ecdsa-p256 Transit key
    When the home key is exchanged through A for token "t1"
    Then token "t1" carries the claims of an API token of the home key
    And the viewer through B with token "t1" is the home key
    And the signing key's row names the Transit key and holds no private half

  Scenario: A Transit key that cannot sign ES256 stops the start
    When an infraweave server signing with an aes256-gcm96 Transit key is started
    Then it exits before it listens, naming the Transit key
