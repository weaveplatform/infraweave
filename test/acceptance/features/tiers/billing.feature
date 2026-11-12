@postgres
Feature: Billing reads with infraweave's meaning
  Spacelift's Billing operations answer from infraweave's own data where a
  read has an honest local meaning, and refuse where only Spacelift's
  billing service could act. tier is the organization's tier as the operator
  defines it, Enterprise while billing is off; seats is the organization's
  pooled seats limit with its use; billedExternally is true,
  billingSubscription and onTrialUntil are null, and the add-on and plan
  lists are empty. The subscription mutations answer 403
  installation-unsupported: the operator sets an organization's tier through
  the operator API.
  ref: decision 0021

  Background:
    Given two infraweave servers, A and B, on one database, with an operator key
    And the operator key is exchanged through A for token "op"
    And the home key is exchanged through B for token "h"

  @op:tier @op:seats @op:billedExternally @op:billingSubscription @op:onTrialUntil @op:availableBillingAddons @op:availableSelfServicePlans @case:success
  Scenario: With billing off the organization reads as Enterprise, without limits
    When token "op" gives the home organization the tier "Free" through A
    Then the tier read through B with token "h" is the tier "Enterprise" as token "op" reads it through A
    And "GET /api/v1/seats" through B with token "h" answers '{"organization":{"inUse":1}}'
    And "GET /api/v1/billed-externally" through A with token "h" answers 'true'
    And "GET /api/v1/billing-subscription" through B with token "h" answers 'null'
    And "GET /api/v1/on-trial-until" through A with token "h" answers 'null'
    And "GET /api/v1/available-billing-addons" through B with token "h" answers '{"items":[]}'
    And "GET /api/v1/available-self-service-plans" through A with token "h" answers '{"items":[]}'

  @op:tier @op:seats @case:success
  Scenario: With billing on the tier is the organization's, and its seats count against the pool
    When token "op" creates the tier "Gold" with the feature "usage" and 5 seats through A
    And token "op" gives the home organization the tier "Gold" through A
    And token "op" turns billing on through A
    Then "GET /api/v1/tier" through B with token "h" eventually answers '{"name":"Gold","features":["usage"],"limits":{"seats":5}}'
    And "GET /api/v1/seats" through B with token "h" answers '{"organization":{"inUse":1,"limit":5}}'

  @op:tier @op:tierFeatures @op:seats @op:billedExternally @op:billingSubscription @op:onTrialUntil @op:availableBillingAddons @op:availableSelfServicePlans @op:billingSubscriptionCreate @op:billingSubscriptionUpdateV2 @op:billingSubscriptionDelete @op:billingSubscriptionUpdateInfo @op:billingSubscriptionUpdateTier @case:denial
  Scenario: A tier without usage refuses every Billing operation
    When token "op" creates the tier "Gold" with the feature "stacks" through A
    And token "op" gives the home organization the tier "Gold" through A
    And token "op" turns billing on through A
    Then "GET /api/v1/tier" through B with token "h" eventually answers a 403 problem "feature-not-in-tier"
    And the problem names the feature "usage"
    And "GET /api/v1/tier-features" through B with token "h" answers a 403 problem "feature-not-in-tier"
    And "GET /api/v1/seats" through B with token "h" answers a 403 problem "feature-not-in-tier"
    And "GET /api/v1/billed-externally" through B with token "h" answers a 403 problem "feature-not-in-tier"
    And "GET /api/v1/billing-subscription" through B with token "h" answers a 403 problem "feature-not-in-tier"
    And "GET /api/v1/on-trial-until" through B with token "h" answers a 403 problem "feature-not-in-tier"
    And "GET /api/v1/available-billing-addons" through B with token "h" answers a 403 problem "feature-not-in-tier"
    And "GET /api/v1/available-self-service-plans" through B with token "h" answers a 403 problem "feature-not-in-tier"
    And "POST /api/v1/billing-subscription" through B with token "h" and '{"input":{"address":"1 Main St","city":"Springfield","country":"US","email":"billing@example.com","fullName":"Pat Doe","zipCode":"12345"},"paymentMethodID":"pm_1"}' answers a 403 problem "feature-not-in-tier"
    And "PUT /api/v1/billing-subscription" through B with token "h" and '{"tier":"V5_BUSINESS"}' answers a 403 problem "feature-not-in-tier"
    And "DELETE /api/v1/billing-subscription" through B with token "h" answers a 403 problem "feature-not-in-tier"
    And "POST /api/v1/billing-subscription/update-info" through B with token "h" and '{"address":"1 Main St","city":"Springfield","country":"US","email":"billing@example.com","fullName":"Pat Doe","zipCode":"12345"}' answers a 403 problem "feature-not-in-tier"
    And "POST /api/v1/billing-subscription/update-tier" through B with token "h" and '{"tier":"V5_BUSINESS"}' answers a 403 problem "feature-not-in-tier"

  @op:billingSubscriptionCreate @op:billingSubscriptionUpdateV2 @op:billingSubscriptionDelete @op:billingSubscriptionUpdateInfo @op:billingSubscriptionUpdateTier @case:failure
  Scenario: Each subscription mutation is refused, as no billing service exists
    Then "POST /api/v1/billing-subscription" through A with token "h" and '{"input":{"address":"1 Main St","city":"Springfield","country":"US","email":"billing@example.com","fullName":"Pat Doe","zipCode":"12345"},"paymentMethodID":"pm_1"}' answers a 403 problem "installation-unsupported"
    And "PUT /api/v1/billing-subscription" through B with token "h" and '{"tier":"V5_BUSINESS"}' answers a 403 problem "installation-unsupported"
    And "DELETE /api/v1/billing-subscription" through A with token "h" answers a 403 problem "installation-unsupported"
    And "POST /api/v1/billing-subscription/update-info" through B with token "h" and '{"address":"1 Main St","city":"Springfield","country":"US","email":"billing@example.com","fullName":"Pat Doe","zipCode":"12345"}' answers a 403 problem "installation-unsupported"
    And "POST /api/v1/billing-subscription/update-tier" through A with token "h" and '{"tier":"V5_BUSINESS"}' answers a 403 problem "installation-unsupported"
    And "GET /api/v1/billing-subscription" through B with token "h" answers 'null'

  @op:tier @op:seats @op:billedExternally @op:billingSubscription @op:onTrialUntil @op:tierFeatures @op:availableBillingAddons @op:availableSelfServicePlans @case:failure
  Scenario: A read without a credential, or with a cursor of another list, is refused
    Then "GET /api/v1/tier" with no credential through A answers a 401 problem "unauthenticated"
    And "GET /api/v1/seats" with no credential through B answers a 401 problem "unauthenticated"
    And "GET /api/v1/billed-externally" with no credential through A answers a 401 problem "unauthenticated"
    And "GET /api/v1/billing-subscription" with no credential through B answers a 401 problem "unauthenticated"
    And "GET /api/v1/on-trial-until" with no credential through A answers a 401 problem "unauthenticated"
    And "GET /api/v1/tier-features?cursor=nope" through B with token "h" answers a 400 problem "invalid-cursor"
    And "GET /api/v1/available-billing-addons?cursor=nope" through A with token "h" answers a 400 problem "invalid-cursor"
    And "GET /api/v1/available-self-service-plans?cursor=nope" through B with token "h" answers a 400 problem "invalid-cursor"
