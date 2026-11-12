@postgres
Feature: Break-glass sign-in rate limit
  Each sign-in draws from the expensive budget of the client's address,
  whichever replica it reaches, so a password guesser is slowed to the
  expensive class's rate.
  ref: decision 0003

  @case:denial
  Scenario: Sign-ins are rate-limited by the client's address
    Given two infraweave servers, A and B, on one database, with an operator key and an expensive budget of 2
    And the password "correct horse" hashed by infraweave auth hash-password
    When the operator key is exchanged through A for token "op"
    And token "op" creates the organization "hooli" with the account "main" and the break-glass login "admin" through A
    Then signing in to "hooli" as "admin" with the password "wrong horse" through A with the public URL's Origin answers a 401 problem "unauthenticated"
    And signing in to "hooli" as "admin" with the password "wrong horse" through B with the public URL's Origin answers a 401 problem "unauthenticated"
    And signing in to "hooli" as "admin" with the password "correct horse" through A with the public URL's Origin answers a 429 problem "rate-limited"
