Feature: Spacelift specification reference
  infraweave mirrors Spacelift's API as REST. The reference in
  api/reference/spacelift projects every Spacelift GraphQL operation onto a
  REST route (research 02) and carries the Terraform provider's attributes
  onto the inputs they set (research 03). These scenarios run against the
  committed snapshots, not fixtures.

  Background:
    Given the committed Spacelift reference

  Scenario: The reference is what its sources generate
    Then the committed spec and coverage report are up to date
    And the spec validates as OpenAPI 3.0.3

  Scenario: No upstream operation is silently dropped
    Then every Spacelift query and mutation is routed or excluded with a reason
    And no mutation is served as an RPC-shaped POST /<operation>

  Scenario Outline: Core resources are REST collections
    Then "<method> <path>" stands for the GraphQL <operation> "<field>"

    Examples: stacks
      | method | path                                  | operation | field       |
      | GET    | /stacks                               | query     | stacks      |
      | POST   | /stacks                               | mutation  | stackCreate |
      | GET    | /stacks/{stackId}                     | query     | stack       |
      | PUT    | /stacks/{stackId}                     | mutation  | stackUpdate |
      | DELETE | /stacks/{stackId}                     | mutation  | stackDelete |
      | POST   | /stacks/search                        | query     | searchStacks |

    Examples: runs nest under their stack
      | method | path                                  | operation | field      |
      | POST   | /stacks/{stackId}/runs                | mutation  | runTrigger |
      | POST   | /stacks/{stackId}/runs/{runId}/cancel | mutation  | runCancel  |
      | GET    | /runs/{runId}                         | query     | run        |

    Examples: attachments
      | method | path                                  | operation | field         |
      | PUT    | /stacks/{stackId}/contexts/{contextId} | mutation | contextAttach |
      | DELETE | /context-attachments/{attachmentId}   | mutation  | contextDetach |

  Scenario Outline: Updates name their target in the path
    Spacelift hides the target's id inside some inputs. REST carries it in
    the path, and the request body drops it.

    Then "PUT <path>" stands for the GraphQL mutation "<field>"
    And path parameter "<param>" of "PUT <path>" carries the GraphQL argument "<argument>"
    And the body of "PUT <path>" does not carry "<dropped>"

    Examples:
      | path                                                       | field                             | param                       | argument             | dropped        |
      | /managed-users/{managedUserId}                             | managedUserUpdate                 | managedUserId               | input.id             | id             |
      | /gitlab-integrations/{gitlabIntegrationId}                 | gitlabIntegrationUpdate           | gitlabIntegrationId         | customInput.id       | customInput.id |
      | /bitbucket-cloud-integrations/{bitbucketCloudIntegrationId} | bitbucketCloudIntegrationUpdateV2 | bitbucketCloudIntegrationId | input.customInput.id | customInput.id |
      | /stack-dependency-references/{referenceId}                 | stackDependenciesUpdateReference  | referenceId                 | reference.id         | id             |

  Scenario Outline: Workflow operations are shaped as resources
    Then "<method> <path>" stands for the GraphQL mutation "<field>"

    Examples:
      | method | path                                                   | field                            |
      | POST   | /stack-dependencies/{stackDependencyId}/references     | stackDependenciesAddReference    |
      | DELETE | /stack-dependency-references/{referenceId}             | stackDependenciesDeleteReference |
      | POST   | /stacks/{stackId}/proposed-runs                        | proposeWithOverrides             |
      | POST   | /runs/batch                                            | runTriggerBatch                  |
      | POST   | /plugins                                               | pluginInstall                    |
      | POST   | /templates/{templateId}/deployments                    | templateDeploymentCreate         |
      | POST   | /notifications/dismiss                                 | dismissNotifications             |
      | PUT    | /intent-projects/{intentProjectId}/configs/{configId}  | intentProjectConfigUpdate        |

  Scenario: Authentication exchanges an API key for a bearer token
    Then "POST /auth/token" stands for the GraphQL mutation "apiKeyUser"
    And "POST /auth/token" needs no bearer token
    And "POST /auth/github/token" needs no bearer token
    And "GET /stacks" needs a bearer token

  Scenario: Terraform attributes annotate the inputs they set
    Then schema "StackInput" property "space" is set by Terraform attribute "spacelift_stack.space_id"
    And schema "StackInput" property "workerPool" is set by Terraform attribute "spacelift_stack.worker_pool_id"
    And schema "StackInput" property "space" has a description

  Scenario: Resources embedded in other resources are references
    Then schema "OpenTofuMigrationQueueEntry" property "stack" refers to "StackRef"
