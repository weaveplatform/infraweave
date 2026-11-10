@postgres
Feature: Roles
  Every account holds five system roles, which never change, and the
  custom roles its account admins define from the action catalogue. A
  custom role's slug is made from its name when it is created and kept
  through every rename. A role never holds a disabled or deprecated
  action: the service refuses a disabled one with 422, and the request's
  validation refuses a deprecated one with 400, since the Action enum
  leaves it out. A role with bindings is not deleted. Any principal of the
  account reads the roles; only an account admin changes them. The home
  key is space-admin on the account's top space; the member key holds
  only the bindings a scenario gives it.

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"

  @op:roles @op:role @case:success
  Scenario: A new account lists its five system roles, space-admin holding every assignable action
    When token "h" sends "GET /api/v1/roles" through A
    Then it answers 200
    And the answer lists the roles "drift-detection-manager, space-admin, space-reader, space-writer, worker-pool-controller"
    And token "h"'s read of the role "space-admin" through B holds every assignable action
    When token "h" sends "GET /api/v1/roles/space-reader" through B
    Then it answers 200
    And the answer's isSystem is 'true'
    And the answer's roleBindingsCount is '0'

  @op:roleCreate @op:role @op:roleUpdate @op:rolePatch @op:roleDelete @case:success
  Scenario: An account admin creates a custom role, renames it, changes its actions and deletes it
    When token "h" sends "POST /api/v1/roles" with '{"name":"Deploy Bots","description":"CI","actions":["STACK_MANAGE","SPACE_READ","STACK_MANAGE"]}' through A
    Then it answers 201
    And the answer's id is '"deploy-bots"'
    And the answer's isSystem is 'false'
    And the answer's actions is '["SPACE_READ","STACK_MANAGE"]'
    When token "h" sends "GET /api/v1/roles/deploy-bots" through B
    Then it answers 200
    And the answer's ETag is kept as "first"
    When token "h" sends "PUT /api/v1/roles/deploy-bots" with '{"name":"Deployers","Actions":["SPACE_WRITE"]}' if matching the ETag "first" through B
    Then it answers 200
    And the answer's id is '"deploy-bots"'
    And the answer's name is '"Deployers"'
    And the answer's description is '"CI"'
    And the answer's actions is '["SPACE_WRITE"]'
    When token "h" sends "PATCH /api/v1/roles/deploy-bots" with '{"description":"Deploys"}' through A
    Then it answers 200
    And the answer's name is '"Deployers"'
    And the answer's description is '"Deploys"'
    When token "h" sends "DELETE /api/v1/roles/deploy-bots" through B
    Then it answers 200
    When token "h" sends "GET /api/v1/roles/deploy-bots" through A
    Then it answers a 404 problem "not-found"

  @op:roles @op:role @op:roleCreate @op:roleUpdate @op:rolePatch @op:roleDelete @case:denial
  Scenario: A member who is not an account admin reads the roles but changes none
    Given token "h" sends "POST /api/v1/roles" with '{"name":"Ops","actions":["SPACE_READ"]}' through A
    And the member key is space-admin on the space "legacy"
    When token "m" sends "GET /api/v1/roles" through B
    Then it answers 200
    And the answer lists the roles "drift-detection-manager, ops, space-admin, space-reader, space-writer, worker-pool-controller"
    When token "m" sends "GET /api/v1/roles/ops" through A
    Then it answers 200
    When token "m" sends "POST /api/v1/roles" with '{"name":"Mine","actions":["SPACE_READ"]}' through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "PUT /api/v1/roles/ops" with '{"name":"Mine"}' through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "PATCH /api/v1/roles/ops" with '{"name":"Mine"}' through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "DELETE /api/v1/roles/ops" through A
    Then it answers a 403 problem "forbidden"

  @op:roles @op:role @op:roleCreate @op:roleUpdate @op:rolePatch @op:roleDelete @case:failure
  Scenario: A disabled or deprecated action, a taken slug, a system role's change and a bound role's delete are refused
    When token "h" sends "POST /api/v1/roles" with '{"name":"Bad","actions":["SPACE_READ","STACK_AWS_INTEGRATION_ATTACH"]}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/actions/1" naming "STACK_AWS_INTEGRATION_ATTACH"
    When token "h" sends "POST /api/v1/roles" with '{"name":"Bad","actions":["UPDATE_BLUEPRINT_DEPLOYMENT"]}' through B
    Then it answers a 400 problem "invalid-request"
    And the problem's detail names "UPDATE_BLUEPRINT_DEPLOYMENT"
    When token "h" sends "POST /api/v1/roles" with '{"name":"Ops","actions":["SPACE_READ"]}' through A
    Then it answers 201
    When token "h" sends "POST /api/v1/roles" with '{"name":"OPS","actions":[]}' through B
    Then it answers a 409 problem "already-exists"
    When token "h" sends "PUT /api/v1/roles/ops" with '{"Actions":["STACK_POLICY_DETACH"]}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/Actions/0" naming "STACK_POLICY_DETACH"
    When token "h" sends "PUT /api/v1/roles/space-admin" with '{"name":"Boss"}' through B
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "PATCH /api/v1/roles/space-reader" with '{"name":"Reader"}' through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "DELETE /api/v1/roles/space-writer" through B
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "GET /api/v1/roles/nobody" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/roles?cursor=nonsense" through B
    Then it answers a 400 problem "invalid-cursor"
    Given the member key is ops on the space "legacy"
    When token "m" sends "GET /api/v1/spaces/legacy" through A
    Then it answers 200
    When token "h" sends "DELETE /api/v1/roles/ops" through B
    Then it answers a 409 problem "invalid-state"
    And the problem's detail names "ops"
