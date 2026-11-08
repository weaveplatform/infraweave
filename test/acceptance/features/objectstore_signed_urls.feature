@postgres
Feature: The fs driver's signed URLs on an authenticating server
  The object store's fs driver serves the URLs it signs on the API listener,
  and the token in the path is their only credential: it binds the method,
  the key, the expiry and a PUT's size. A client holding one sends no
  Authorization header, so the routes take none, and a token that does not
  verify answers the driver's 403, never the bearer check's 401. Every
  other route still needs a credential. No operation hands out a signed
  URL yet, so the steps sign one as the servers' driver does, under the
  keyring of their database.
  ref: decision 0005

  Background:
    Given two infraweave servers, A and B, on one database

  Scenario: A signed URL stores and reads an object with no credential
    Then "terraform state" put with no credential at a URL signed for "acct/upload-1" through A answers 200
    And a URL signed for reading "acct/upload-1" through B reads "terraform state" with no credential

  Scenario: A signed URL whose token does not verify answers 403, not 401
    Then a tampered URL signed for PUT of "acct/upload-2" through A answers a 403 problem "forbidden"
    And an expired URL signed for PUT of "acct/upload-2" through A answers a 403 problem "forbidden"
    And a tampered URL signed for GET of "acct/upload-2" through B answers a 403 problem "forbidden"
    And an expired URL signed for GET of "acct/upload-2" through B answers a 403 problem "forbidden"
    And "GET /blobs" with no credential through A answers a 401 problem "unauthenticated"
