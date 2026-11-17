@postgres @objectstore
Feature: Signed state upload and download URLs
  stateUploadUrl signs a URL that takes one PUT of a state file with no
  credential, which infraweave keeps sealed in its uploads store under
  the object ID it answers beside the URL; a second PUT to the URL, or one
  whose token was altered, is refused. stateDownloadUrl signs a URL that
  reads a stack's current state version, for a caller holding
  STACK_STATE_READ beside the route's STACK_STATE_DOWNLOAD, on a stack
  whose state infraweave keeps with external state access on. Each URL is
  absolute under server.public_url, which no client of the scenario
  resolves, so the scenario sends its path to either replica. The backend
  feature's apply reads its state back through a download URL. Either URL
  is refused 15 minutes after it was signed, and an upload no import took
  within a day expires through blob.expire, after which its object ID
  imports nothing. A real server's clock cannot be moved, so time passes
  for a URL by signing its token again with an earlier expiry, under the
  scenario's keyring, and for an upload by moving its creation and expiry
  back. The home key is space-admin on the account's top space; the member
  key holds only the bindings a scenario gives it. Stack infra keeps its
  state with external state access on, closed keeps it with access off,
  and plain does not keep it.
  ref: decision 0010
  ref: decision 0016

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy","vendorConfig":{"terraform":{"externalStateAccessEnabled":true}}},"manageState":true}' through A
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Closed","repository":"infra","branch":"main","space":"legacy"},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Plain","repository":"infra","branch":"main","space":"legacy"},"manageState":false}' through A

  @op:stateUploadUrl @case:success
  Scenario: An upload URL takes one state file with no credential and keeps it sealed
    When token "h" sends "POST /api/v1/state/upload-url" with '{}' through A
    Then it answers 200
    And the answer's uploadHeaders is '{"entries":[]}'
    And the answer's url is kept as "up"
    When '{"version":4,"serial":1,"lineage":"l","outputs":{"token":{"value":"plain-up10ad-0506","type":"string","sensitive":true}},"resources":[]}' is put with no credential at the URL "up" through B
    Then it answers 200
    And the upload of the URL "up" is one sealed object in the uploads store
    And no object in the scenario's bucket holds "plain-up10ad-0506", of at least 1 object

  @op:stateUploadUrl @case:denial
  Scenario: A caller without STACK_MANAGED_STATE_IMPORT signs no upload URL
    Given the member key is space-writer on the space "legacy"
    When token "m" sends "POST /api/v1/state/upload-url" with '{}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_MANAGED_STATE_IMPORT"'

  @op:stateUploadUrl @case:failure
  Scenario: An upload URL refuses a tampered token and a second upload
    Given token "h" sends "POST /api/v1/state/upload-url" with '{}' through A
    And the answer's url is kept as "up"
    When '{"version":4}' is put with no credential at the URL "up" tampered through A
    Then it answers a 403 problem "forbidden"
    When '{"version":4}' is put with no credential at the URL "up" through B
    Then it answers 200
    When '{"version":4}' is put with no credential at the URL "up" through A
    Then it answers a 409 problem "invalid-state"
    And the upload of the URL "up" is one sealed object in the uploads store

  @op:stateDownloadUrl @case:denial
  Scenario: A caller needs STACK_STATE_DOWNLOAD and STACK_STATE_READ to sign a download URL
    Given the member key is space-reader on the space "legacy"
    When token "m" sends "POST /api/v1/stacks/infra/state/download-url" with '{}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_STATE_DOWNLOAD"'
    Given token "h" sends "POST /api/v1/roles" with '{"name":"State Downloaders","actions":["SPACE_READ","STACK_STATE_DOWNLOAD"]}' through B
    And the member key is state-downloaders on the space "legacy"
    When token "m" sends "POST /api/v1/stacks/infra/state/download-url" with '{}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_STATE_READ"'

  @op:stateDownloadUrl @case:failure
  Scenario: A download URL is refused for a named version, a stack with no state, one with external access off and one whose state infraweave does not keep
    When token "h" sends "POST /api/v1/stacks/infra/state/download-url" with '{"version":"01"}' through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "POST /api/v1/stacks/infra/state/download-url" with '{}' through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/stacks/closed/state/download-url" with '{}' through A
    Then it answers a 403 problem "forbidden"
    When token "h" sends "POST /api/v1/stacks/plain/state/download-url" with '{}' through B
    Then it answers a 404 problem "not-found"

  @op:stateUploadUrl @op:stateDownloadUrl @case:failure
  Scenario: An upload or download URL is refused once 15 minutes have passed since it was signed
    Given token "h" sends "POST /api/v1/state/upload-url" with '{}' through A
    And the answer's url is kept as "up"
    And the answer's objectId names "obj"
    And token "h" sends "POST /api/v1/state/upload-url" with '{}' through B
    And the answer's url is kept as "late"
    When 15 minutes pass for the URL "late"
    And '{"version":4,"serial":1,"lineage":"l","outputs":{},"resources":[]}' is put with no credential at the URL "late" through A
    Then it answers a 403 problem "forbidden"
    When 14 minutes pass for the URL "up"
    And '{"version":4,"serial":1,"lineage":"l","outputs":{},"resources":[]}' is put with no credential at the URL "up" through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Copy","repository":"infra","branch":"main","space":"legacy","vendorConfig":{"terraform":{"externalStateAccessEnabled":true}}},"manageState":true,"stackObjectID":"{obj}"}' through A
    Then it answers 201
    When token "h" sends "POST /api/v1/stacks/copy/state/download-url" with '{}' through B
    Then it answers 200
    And the answer's url is kept as "dl"
    When 14 minutes pass for the URL "dl"
    Then the URL "dl" read with no credential through A is the current state version of the stack "copy"
    When 1 minute passes for the URL "dl"
    Then the URL "dl" read with no credential through B answers a 403 problem "forbidden"

  @op:stackCreate @op:stackManagedStateImport @case:failure
  Scenario: An upload no import took within a day expires, and its object ID then imports nothing
    Given token "h" sends "POST /api/v1/state/upload-url" with '{}' through A
    And the answer's url is kept as "young"
    And the answer's objectId names "young"
    And '{"version":4,"serial":1,"lineage":"l","outputs":{},"resources":[]}' is put with no credential at the URL "young" through B
    And token "h" sends "POST /api/v1/state/upload-url" with '{}' through B
    And the answer's url is kept as "old"
    And the answer's objectId names "old"
    And '{"version":4,"serial":1,"lineage":"l","outputs":{},"resources":[]}' is put with no credential at the URL "old" through A
    When 23 hours pass for the upload of the URL "young"
    And 24 hours pass for the upload of the URL "old"
    And the "blob.expire" periodic task falls due
    Then "blob.expire" eventually runs once more, as one job
    And the upload of the URL "old" is gone from the uploads store
    And the upload of the URL "young" is one sealed object in the uploads store
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Copy","repository":"infra","branch":"main","space":"legacy"},"manageState":true,"stackObjectID":"{old}"}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/stackObjectID" as "not-found"
    When token "h" sends "POST /api/v1/stacks/infra/lock" through A
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/managed-state-import" with '{"state":"{old}"}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/state" as "not-found"
    When token "h" sends "POST /api/v1/stacks/infra/managed-state-import" with '{"state":"{young}"}' through A
    Then the answer is 'true'
