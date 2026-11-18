@postgres @azure-devops
Feature: Azure DevOps service hook intake
  Azure identifies deliveries and event types in the body. Empty-username Basic
  authenticates each request; retained signatures bind the exact body without
  retaining the password. Two actual all processes share the durable inbox.
  Normalization carries payload facts without evaluating comments or policies.

  # ref: decision 0012
  # ref: decision 0013
  # ref: decision 0016

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And an Azure DevOps user "dev" with a personal access token kept as "tok"
    And an Azure DevOps repository "Platform/infra" that "dev" administers, its head of "main" kept as "seed"
    And token "h" sends "POST /api/v1/azure-dev-ops-repo-integrations" with '{"organizationURL":"{azureDevOpsOrg}","personalAccessToken":"{tok}","customInput":{"name":"Hooks","spaceID":"acme","isDefault":true}}' through A
    And it answers 201
    And the answer's id names "integration"
    And the Azure webhook password is captured privately

  Scenario: Push, forked pull request and nested pull request comment are durable
    Given an Azure push webhook named "azure-push-539" is prepared
    When the Azure webhook is sent with valid Basic credentials through A
    Then it answers 202
    And the webhook answer is empty with the shared headers
    And the Azure webhook is retained with its exact body HMAC and reaches dispatched
    When the Azure webhook is sent with valid Basic credentials through B
    Then it answers 200
    And there are 1 Azure webhook deliveries
    Given an Azure request webhook named "azure-request-539" is prepared
    When the Azure webhook is sent with valid Basic credentials through B
    Then it answers 202
    And the Azure webhook is retained with its exact body HMAC and reaches dispatched
    Given an Azure comment webhook named "azure-comment-539" is prepared
    When the Azure webhook is sent with valid Basic credentials through A
    Then it answers 202
    And the Azure webhook is retained with its exact body HMAC and reaches dispatched
    And there are 3 Azure webhook deliveries
    And Azure webhook credentials never reach rows or server logs

  Scenario: Wrong, missing and named Basic credentials are refused before filtering
    Given an Azure unsupported webhook named "azure-refused-539" is prepared
    When the Azure webhook is sent with wrong Basic credentials through A
    Then it answers 401
    And the webhook answer is empty with the shared headers
    When the Azure webhook is sent with missing Basic credentials through B
    Then it answers 401
    And the webhook answer is empty with the shared headers
    When the Azure webhook is sent with named Basic credentials through A
    Then it answers 401
    And the webhook answer is empty with the shared headers
    And there are 0 Azure webhook deliveries
    When the Azure webhook is sent with valid Basic credentials through B
    Then it answers 200
    And the webhook answer is empty with the shared headers
    And there are 0 Azure webhook deliveries
    And Azure webhook credentials never reach rows or server logs

  Scenario: An authenticated envelope without an event type fails durably
    Given an Azure invalid webhook named "azure-invalid-539" is prepared
    When the Azure webhook is sent with valid Basic credentials through A
    Then it answers 202
    And the webhook answer is empty with the shared headers
    And the Azure webhook is retained with its exact body HMAC and reaches failed parsing
    And there are 1 Azure webhook deliveries
    And Azure webhook credentials never reach rows or server logs

  Scenario: A consumed event with malformed data is retained for diagnosis
    Given an Azure malformed webhook named "azure-malformed-539" is prepared
    When the Azure webhook is sent with valid Basic credentials through B
    Then it answers 202
    And the webhook answer is empty with the shared headers
    And the Azure webhook is retained with its exact body HMAC and reaches failed parsing
    And there are 1 Azure webhook deliveries
    And Azure webhook credentials never reach rows or server logs
