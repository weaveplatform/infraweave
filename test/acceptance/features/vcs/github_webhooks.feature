@postgres @github
Feature: Account-owned GitHub Apps deliver verified webhooks
  Every GitHub App integration uses its own webhook secret on the shared
  API listener. The fake sends each request once, and redelivery keeps its
  original ID and signed bytes. The same integration route serves apps on
  github.com and GitHub Enterprise Server. Registration fetches metadata
  from the fake; the fixture then sets the actual API host before delivery,
  so the public-host case makes no request to live GitHub.

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a GitHub App "hook" installed on the organization "acme"

  Scenario Outline: A signed push is durable once through either account-owned GitHub host
    When token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","userFacingHost":"<host>","appID":"{hookID}","privateKey":"{hookKey}","webhookSecret":"{hookWebhookSecret}","customInput":{"name":"Webhook App","spaceID":"acme","isDefault":true}}' through A
    Then it answers 201
    And the answer's id names "integration"
    And the answer's userFacingHost is '"<host>"'
    And the GitHub integration "integration" uses the stored API host "<apiHost>"
    When the GitHub App "hook" sends "push" action "" as delivery "push-first" to integration "integration" through A
    Then it answers 202
    And the GitHub App "hook" recorded only the attempt statuses "202"
    And GitHub delivery "push-first" belongs to integration "integration" with its exact signed bytes and one processing job
    And GitHub delivery "push-first" is eventually dispatched
    When the GitHub App "hook" sends "push" action "" as delivery "push-first" to integration "integration" through B
    Then it answers 200
    When the GitHub App "hook" redelivers its first delivery
    Then it answers 200
    And the GitHub App "hook" recorded only the attempt statuses "202, 200, 200"
    And GitHub delivery "push-first" belongs to integration "integration" with its exact signed bytes and one processing job
    And no stored webhook row or server log contains the GitHub App secrets "hook"

    Examples:
      | host               | apiHost                |
      | https://github.com | https://api.github.com |
      | {githubHost}       | {githubHost}           |

  Scenario: A valid but unsupported pull request action is filtered before persistence
    Given token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{hookID}","privateKey":"{hookKey}","webhookSecret":"{hookWebhookSecret}","customInput":{"name":"Filtered App","spaceID":"acme","isDefault":true}}' through A
    And it answers 201
    And the answer's id names "integration"
    When the GitHub App "hook" sends "pull_request" action "assigned" as delivery "filtered-action" to integration "integration" through B
    Then it answers 200
    And the GitHub App "hook" recorded only the attempt statuses "200"
    And no webhook delivery, processing job or inbox object is stored
    And no stored webhook row or server log contains the GitHub App secrets "hook"

  Scenario: Another App's signature is refused without storage or automatic retry
    Given token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{hookID}","privateKey":"{hookKey}","webhookSecret":"{hookWebhookSecret}","customInput":{"name":"Verified App","spaceID":"acme","isDefault":true}}' through A
    And it answers 201
    And the answer's id names "integration"
    And a GitHub App "wrong" installed on the organization "acme"
    When the GitHub App "wrong" sends "push" action "" as delivery "bad-signature" to integration "integration" through A
    Then it answers 401
    And the GitHub App "wrong" recorded only the attempt statuses "401"
    When the GitHub App "wrong" redelivers its first delivery
    Then it answers 401
    And the GitHub App "wrong" recorded only the attempt statuses "401, 401"
    And no webhook delivery, processing job or inbox object is stored
    And no stored webhook row or server log contains the GitHub App secrets "hook, wrong"
