@policy-push-failures @postgres @objectstore @github @gitlab @bitbucket-cloud @bitbucket-dc @azure-devops
Feature: Push policy failures have durable native feedback
  Invalid outputs, runtime errors and conflicting lock or priority decisions
  create no run and commit safe internal_error metadata with a native failed
  check. Provisional cancellation and policy locks do not escape failure.
  ref: decision 0009
  ref: decision 0012
  ref: decision 0016

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"

  Scenario Outline: <provider> fails closed for <failure>
    Given a "<provider>" plan-comment stack "infra" using "terraform" with labels '[]'
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And it answers 200
    And the answer's id names "prior"
    And token "h" eventually reads the run "prior" in the state "READY" through B
    And token "h" sends "POST /api/v1/policies" with '<first>' through A
    And it answers 201
    And the answer's id names "first"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{first}" through B
    And it answers 200
    And token "h" sends "POST /api/v1/policies" with '<second>' through A
    And it answers 201
    And the answer's id names "second"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{second}" through B
    And it answers 200
    When the event repository commits "errored" on "main"
    And the event fixture pushes "failure" from "seed" to "errored" on "main" with "valid" verification through A and receives 202
    Then event delivery "failure" settles with 0 new runs
    And the failed push on commit "errored" has durable native feedback and internal_error with no effects

    Examples:
      | provider | failure | first | second |
      | GitHub | priority conflict | {"name":"First","type":"GIT_PUSH","body":"package infraweave\\ntrack := true\\npriority := \\"high\\"\\ncancel contains \\"{prior}\\"\\n"} | {"name":"Second","type":"GIT_PUSH","body":"package infraweave\\npriority := \\"low\\"\\ncancel contains \\"{prior}\\"\\n"} |
      | GitHub | lock conflict | {"name":"First","type":"GIT_PUSH","body":"package infraweave\\ntrack := true\\nlock := \\"first\\"\\ncancel contains \\"{prior}\\"\\n"} | {"name":"Second","type":"GIT_PUSH","body":"package infraweave\\nlock := \\"second\\"\\ncancel contains \\"{prior}\\"\\n"} |
      | GitHub | invalid output | {"name":"First","type":"GIT_PUSH","body":"package infraweave\\ntrack := true\\npriority := \\"invalid\\"\\ncancel contains \\"{prior}\\"\\n"} | {"name":"Second","type":"GIT_PUSH","body":"package infraweave\\nflag contains \\"metadata\\"\\ncancel contains \\"{prior}\\"\\n"} |
      | GitHub | runtime failure | {"name":"First","type":"GIT_PUSH","body":"package infraweave\\ntrack := true if input.push.branch == \\"main\\"\\ntrack := false if input.push.hash != \\"\\"\\ncancel contains \\"{prior}\\"\\n"} | {"name":"Second","type":"GIT_PUSH","body":"package infraweave\\nflag contains \\"metadata\\"\\ncancel contains \\"{prior}\\"\\n"} |
      | GitLab | priority conflict | {"name":"First","type":"GIT_PUSH","body":"package infraweave\\ntrack := true\\npriority := \\"high\\"\\ncancel contains \\"{prior}\\"\\n"} | {"name":"Second","type":"GIT_PUSH","body":"package infraweave\\npriority := \\"low\\"\\ncancel contains \\"{prior}\\"\\n"} |
      | GitLab | lock conflict | {"name":"First","type":"GIT_PUSH","body":"package infraweave\\ntrack := true\\nlock := \\"first\\"\\ncancel contains \\"{prior}\\"\\n"} | {"name":"Second","type":"GIT_PUSH","body":"package infraweave\\nlock := \\"second\\"\\ncancel contains \\"{prior}\\"\\n"} |
      | GitLab | invalid output | {"name":"First","type":"GIT_PUSH","body":"package infraweave\\ntrack := true\\npriority := \\"invalid\\"\\ncancel contains \\"{prior}\\"\\n"} | {"name":"Second","type":"GIT_PUSH","body":"package infraweave\\nflag contains \\"metadata\\"\\ncancel contains \\"{prior}\\"\\n"} |
      | GitLab | runtime failure | {"name":"First","type":"GIT_PUSH","body":"package infraweave\\ntrack := true if input.push.branch == \\"main\\"\\ntrack := false if input.push.hash != \\"\\"\\ncancel contains \\"{prior}\\"\\n"} | {"name":"Second","type":"GIT_PUSH","body":"package infraweave\\nflag contains \\"metadata\\"\\ncancel contains \\"{prior}\\"\\n"} |
      | Bitbucket Cloud | priority conflict | {"name":"First","type":"GIT_PUSH","body":"package infraweave\\ntrack := true\\npriority := \\"high\\"\\ncancel contains \\"{prior}\\"\\n"} | {"name":"Second","type":"GIT_PUSH","body":"package infraweave\\npriority := \\"low\\"\\ncancel contains \\"{prior}\\"\\n"} |
      | Bitbucket Cloud | lock conflict | {"name":"First","type":"GIT_PUSH","body":"package infraweave\\ntrack := true\\nlock := \\"first\\"\\ncancel contains \\"{prior}\\"\\n"} | {"name":"Second","type":"GIT_PUSH","body":"package infraweave\\nlock := \\"second\\"\\ncancel contains \\"{prior}\\"\\n"} |
      | Bitbucket Cloud | invalid output | {"name":"First","type":"GIT_PUSH","body":"package infraweave\\ntrack := true\\npriority := \\"invalid\\"\\ncancel contains \\"{prior}\\"\\n"} | {"name":"Second","type":"GIT_PUSH","body":"package infraweave\\nflag contains \\"metadata\\"\\ncancel contains \\"{prior}\\"\\n"} |
      | Bitbucket Cloud | runtime failure | {"name":"First","type":"GIT_PUSH","body":"package infraweave\\ntrack := true if input.push.branch == \\"main\\"\\ntrack := false if input.push.hash != \\"\\"\\ncancel contains \\"{prior}\\"\\n"} | {"name":"Second","type":"GIT_PUSH","body":"package infraweave\\nflag contains \\"metadata\\"\\ncancel contains \\"{prior}\\"\\n"} |
      | Bitbucket DC | priority conflict | {"name":"First","type":"GIT_PUSH","body":"package infraweave\\ntrack := true\\npriority := \\"high\\"\\ncancel contains \\"{prior}\\"\\n"} | {"name":"Second","type":"GIT_PUSH","body":"package infraweave\\npriority := \\"low\\"\\ncancel contains \\"{prior}\\"\\n"} |
      | Bitbucket DC | lock conflict | {"name":"First","type":"GIT_PUSH","body":"package infraweave\\ntrack := true\\nlock := \\"first\\"\\ncancel contains \\"{prior}\\"\\n"} | {"name":"Second","type":"GIT_PUSH","body":"package infraweave\\nlock := \\"second\\"\\ncancel contains \\"{prior}\\"\\n"} |
      | Bitbucket DC | invalid output | {"name":"First","type":"GIT_PUSH","body":"package infraweave\\ntrack := true\\npriority := \\"invalid\\"\\ncancel contains \\"{prior}\\"\\n"} | {"name":"Second","type":"GIT_PUSH","body":"package infraweave\\nflag contains \\"metadata\\"\\ncancel contains \\"{prior}\\"\\n"} |
      | Bitbucket DC | runtime failure | {"name":"First","type":"GIT_PUSH","body":"package infraweave\\ntrack := true if input.push.branch == \\"main\\"\\ntrack := false if input.push.hash != \\"\\"\\ncancel contains \\"{prior}\\"\\n"} | {"name":"Second","type":"GIT_PUSH","body":"package infraweave\\nflag contains \\"metadata\\"\\ncancel contains \\"{prior}\\"\\n"} |
      | Azure DevOps | priority conflict | {"name":"First","type":"GIT_PUSH","body":"package infraweave\\ntrack := true\\npriority := \\"high\\"\\ncancel contains \\"{prior}\\"\\n"} | {"name":"Second","type":"GIT_PUSH","body":"package infraweave\\npriority := \\"low\\"\\ncancel contains \\"{prior}\\"\\n"} |
      | Azure DevOps | lock conflict | {"name":"First","type":"GIT_PUSH","body":"package infraweave\\ntrack := true\\nlock := \\"first\\"\\ncancel contains \\"{prior}\\"\\n"} | {"name":"Second","type":"GIT_PUSH","body":"package infraweave\\nlock := \\"second\\"\\ncancel contains \\"{prior}\\"\\n"} |
      | Azure DevOps | invalid output | {"name":"First","type":"GIT_PUSH","body":"package infraweave\\ntrack := true\\npriority := \\"invalid\\"\\ncancel contains \\"{prior}\\"\\n"} | {"name":"Second","type":"GIT_PUSH","body":"package infraweave\\nflag contains \\"metadata\\"\\ncancel contains \\"{prior}\\"\\n"} |
      | Azure DevOps | runtime failure | {"name":"First","type":"GIT_PUSH","body":"package infraweave\\ntrack := true if input.push.branch == \\"main\\"\\ntrack := false if input.push.hash != \\"\\"\\ncancel contains \\"{prior}\\"\\n"} | {"name":"Second","type":"GIT_PUSH","body":"package infraweave\\nflag contains \\"metadata\\"\\ncancel contains \\"{prior}\\"\\n"} |
