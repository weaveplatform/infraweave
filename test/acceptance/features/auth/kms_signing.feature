@identity @postgres @vault
Feature: Tokens signed through a KMS
  With auth.signing.backend kms, the signing keys live in a KMS the
  operator names, the api-token keys in auth.signing.kms_keys and the
  federation keys in auth.federation.kms_keys, and infraweave signs
  through it: the private half never enters the process or the database.
  signing_keys holds each key's reference in place of a sealed private
  half. Each server opens every reference at start and refuses to start
  with a key that cannot sign its purpose's algorithm, ES256 for api-token
  and auth.federation.algorithm, RS256 by default, for federation, or
  without a federation key at all, naming the key or the setting. The
  scenarios sign with versions of Transit keys on the @vault container,
  which the servers reach through secrets.kek.vault's address and token
  while the KEK stays local.
  ref: decision 0006

  Scenario: A token signed through Transit by one replica verifies on the other
    Given two infraweave servers, A and B, on one database, signing with an ecdsa-p256 Transit key
    When the home key is exchanged through A for token "t1"
    Then token "t1" carries the claims of an API token of the home key
    And the viewer through B with token "t1" is the home key
    And the signing key's row names the Transit key and holds no private half
    And the federation key's row names the rsa-3072 Transit key, signs RS256 and holds no private half

  Scenario: A Transit key that cannot sign ES256 stops the start
    When an infraweave server signing with an aes256-gcm96 Transit key is started
    Then it exits before it listens, naming the Transit key and ES256

  Scenario Outline: A federation Transit key that cannot sign RS256 stops the start
    When an infraweave server signing federation tokens with an <type> Transit key is started
    Then it exits before it listens, naming the federation Transit key and RS256

    Examples:
      | type       |
      | ecdsa-p256 |
      | rsa-2048   |

  Scenario: The kms backend without federation keys stops the start
    When an infraweave server signing with an ecdsa-p256 Transit key and no federation key is started
    Then it exits before it listens, naming auth.federation.kms_keys
