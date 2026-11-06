@postgres
Feature: One rate-limit budget across replicas
  Two infraweave serve processes on one database draw each caller's tokens
  from one bucket (decisions 0003 and 0005), so X-RateLimit-Remaining is
  right whichever replica answers, and the 429's Retry-After is the wait for
  one token at the bucket's rate. Each replica's client holds a token of its
  own from one API key, and the caller is the key, api_key:<key ID>
  (decision 0006), so the two tokens draw on one bucket.

  Scenario: Requests through both replicas draw on one bucket
    Given two infraweave servers, A and B, on one database whose standard budget is 4 at 0.01 a second
    Then "GET /api/v1/stacks" through A leaves 3 requests in the budget
    And "GET /api/v1/stacks" through B leaves 2 requests in the budget
    And "GET /api/v1/stacks" through A leaves 1 request in the budget
    And "GET /api/v1/stacks" through B leaves 0 requests in the budget
    And "GET /api/v1/stacks" through A answers a 429 problem "rate-limited"
    And its Retry-After is 100 seconds, give or take 10
    And "GET /api/v1/stacks" through B answers a 429 problem "rate-limited"
