@kubernetes-lab @kubernetes
Feature: Native worker resource schemas
  These cases exercise actual Kubernetes admission and persistence. Explicit
  status fixtures prove schema transitions, not remote pool creation or execution.

  Background:
    Given the owned Terraform Kubernetes API and namespace are ready
    And the canonical worker CRDs are established in the owned cluster

  Scenario: Typed pool settings reject unsafe and malformed inputs
    When the real Kubernetes API rejects these worker pool inputs:
      | field                                  | value                                                                                              | status |
      | spec.capacity                          | -1                                                                                                 | 422    |
      | spec.capacity                          | 2147483648                                                                                         | 422    |
      | spec.capacity                          | true                                                                                               | 422    |
      | spec.mode                              | "Public"                                                                                           | 422    |
      | spec.mode                              | "Existing"                                                                                         | 422    |
      | spec.pod.resources.requests.cpu         | "invalid"                                                                                          | 422    |
      | spec.pod.resources.requests.cpu         | "3"                                                                                                | 422    |
      | spec.pod.storageSize                   | "0"                                                                                                | 422    |
      | spec.pod.resources.requests            | {}                                                                                                 | 422    |
      | spec.pod.privileged                     | true                                                                                               | 400    |
      | spec.credentialsRef.namespace          | "another-namespace"                                                                                | 400    |
      | spec.privateKey                        | "embedded-private-key"                                                                             | 400    |
      | spec.environment                       | [{"name":"VALUE","exposure":"root"}]                                                                | 422    |
      | spec.environment                       | [{"name":"VALUE","exposure":"all"}]                                                                 | 422    |
      | spec.environment                       | [{"name":"ro_TOKEN","exposure":"write","from":{"kind":"Secret","name":"inputs","key":"token"}}]       | 422    |
      | spec.environment                       | [{"name":"TOKEN","exposure":"read","from":{"kind":"Secret","name":"inputs","key":"token"}}]           | 422    |
      | spec.environment                       | [{"name":"wo_","exposure":"write","from":{"kind":"Secret","name":"inputs","key":"token"}}]            | 422    |
      | spec.environment                       | [{"name":"TOKEN","exposure":"all","from":{"kind":"PVC","name":"data","key":"token"}}]                 | 422    |
      | spec.environment                       | [{"name":"TOKEN","exposure":"all","from":{"kind":"Secret","name":"pool-credentials","key":"privateKey"}}] | 422 |
      | spec.mounts                            | [{"kind":"PVC","name":"data","target":"/data","exposure":"all","readOnly":false}]                     | 422    |
      | spec.mounts                            | [{"kind":"Secret","name":"data","target":"/data","exposure":"all","readOnly":true}]                    | 422    |
      | spec.mounts                            | [{"kind":"PVC","name":"data","target":"/opt/infraweave/tools","exposure":"all","readOnly":true}]         | 422    |
      | spec.mounts                            | [{"kind":"Secret","name":"pool-credentials","key":"privateKey","target":"/data","exposure":"all","readOnly":true}] | 422 |
      | spec.mounts                            | [{"kind":"PVC","name":"data","target":"/opt//infraweave/tools","exposure":"all","readOnly":true}]        | 422    |
      | spec.mounts                            | [{"kind":"PVC","name":"data","target":"/opt/../bin/terraform","exposure":"all","readOnly":true}]         | 422    |
      | spec.mounts                            | [{"kind":"PVC","name":"data","target":"/data","exposure":"read","readOnly":true}]                      | 422    |
      | spec.mounts                            | [{"kind":"Secret","name":"inputs","key":"token","target":"/mnt/workspace/token","exposure":"read","readOnly":true}] | 422 |

  Scenario: Unknown workload escape fields are rejected or safely pruned
    When strict unknown fields are refused and non-strict fields are pruned

  Scenario: Correctable references and immutable bound identity have distinct lifecycles
    When worker pool identity survives repair and becomes immutable when bound

  Scenario: A worker cannot move to another parent, pool or logical slot
    When worker resource parent and slot identities are immutable

  Scenario: Managed, Existing and Public schemas retain the full logical-capacity bounds
    When every pool mode accepts valid zero and maximum logical capacities

  Scenario: Native structural constraints reject the unsupported closed-object schema form
    When the real Kubernetes API refuses a nonstructural closed-object schema
