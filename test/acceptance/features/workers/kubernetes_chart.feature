@kubernetes-lab @kubernetes @postgres @vcs
Feature: The Kubernetes worker chart
  The infraweave-workers chart installs a worker controller in the owned
  cluster through Helm's SDK, as helm install does: its Deployment of two
  replicas of the image the suite imports into the node, holding the
  build under test, which is also each run Pod's bootstrap image, its
  release-scoped Lease, service account and Roles, the network policies
  of the controller and of its runs, and its WorkerPools. Each release
  watches its own namespace, with a Managed pool whose credential Secret
  and management key exist before it installs, and trusts the scenario's
  HTTPS front, which balances the scenario's servers, through a Secret.
  A controller is ready once the API server answers its Lease and, on
  the leader, once each pool's installation answers. Uninstalling drains
  a release: its uninstall Job deletes the release's pools and waits for
  its controller to finalize them before Helm deletes the controller.
  Values the chart refuses, and references to Secrets that do not exist,
  fail the install before anything is created. Repositories are Gitea's;
  the home key is space-admin on the account's top space.
  ref: decision 0008
  ref: decision 0013
  ref: decision 0020

  Background:
    Given the owned Terraform Kubernetes API and namespace are ready
    And the canonical worker CRDs are established in the owned cluster
    And a controller namespace "home" holds the home management key
    And a controller namespace "other" holds the home management key

  @terraform @op:workerPoolCreate @op:workerPoolManaged @op:workerRegister @op:workerPoolDelete @op:runProposeLocalWorkspace @case:success
  Scenario: Two releases run real work side by side, survive an upgrade and a restart, and one drains as it uninstalls
    Given run Pods reach the scenario's servers, and the objects they sign, through the HTTPS front
    And the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "this" {\n  input = "one"\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    When the worker chart is installed as "alpha" in "home" with the Managed pool "build" of capacity 1 in the space "team"
    And the worker chart is installed as "beta" in "other" with the Managed pool "small" of capacity 1 in the space "team"
    Then the worker chart release "alpha" eventually has 2 ready controllers
    And the worker chart release "beta" eventually has 2 ready controllers
    And the controllers of "alpha" run the imported image and log its commit and FIPS 140-3 mode
    And the WorkerPool "build" eventually has 1 registered slots, its pool kept as "build"
    And the WorkerPool "small" eventually has 1 registered slots, its pool kept as "small"
    And the WorkerPool "build" eventually reports ExecutorAvailable True "LauncherRunning"
    And the WorkerPools "build" and "small" hold distinct pools and tokens
    And the controller account of "alpha" is refused Secrets, WorkerPools and Pods in "other" and Secret listing in its own
    And the worker chart install of "gamma" in "other" with the values '{"watchNamespaces":["{home}"],"pools":[]}' is refused naming "cannot be imported into the current release"
    When the HTTPS front answers 502 until the worker chart release "alpha" has 1 ready controller
    Then the worker chart release "alpha" eventually has 2 ready controllers
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"{team}","workerPool":"{build}","localPreviewEnabled":true,"vendorConfig":{"terraform":{}}},"manageState":false}' through B
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And a local workspace "ws" holding the files:
      | main.tf | resource "terraform_data" "local" {\n  input = "uncommitted"\n}\n |
    When token "h" sends "POST /api/v1/stacks/infra/local-workspace-uploads" with '{"size":{ws.size},"sha256":"{ws.sha256}"}' through A
    Then it answers 201
    And the answer's id names "upload"
    And the local workspace "ws" is put at the answer's uploadUrl
    When token "h" sends "POST /api/v1/stacks/infra/runs/propose-local-workspace" with '{"workspace":"{upload}"}' through B
    Then it answers 200
    And the answer's id names "preview"
    And token "h" eventually reads the run "preview" in the state "FINISHED" through A
    And token "h" reads the run "preview"'s PLANNING log through B, which holds "terraform_data.local will be created" and not "Error"
    And the run "preview" ran in Pods of the WorkerPool "build" from the image "{runner.terraform}", each hardened and holding no "TF_VAR_secret"
    And the run "preview" eventually has no Pod
    And the slots of the WorkerPool "build" are eventually idle
    When the worker chart release "alpha" is upgraded with the pool "build" of capacity 2 and debug logs
    Then the WorkerPool "build" eventually has 2 registered slots, its pool kept as "same"
    And the worker chart release "alpha" eventually has 2 ready controllers
    When token "h" sends "GET /api/v1/worker-pools/{same}" through B
    Then the answer's id is '"{build}"'
    And the answer's workersCount is '2'
    When the controller Pods of "alpha" are deleted
    Then the worker chart release "alpha" eventually has 2 ready controllers
    And the workers of the WorkerPool "build" are the ones kept
    And the WorkerPool "build" eventually reports ExecutorAvailable True "LauncherRunning"
    When token "h" sends "DELETE /api/v1/stacks/infra" through A
    Then it answers 200
    When the worker chart release "alpha" is uninstalled
    Then the worker chart release "alpha" left only the worker CRDs
    When token "h" sends "GET /api/v1/worker-pools/{build}" through A
    Then it answers a 404 problem "not-found"
    And the WorkerPool "small" eventually reports ExecutorAvailable True "LauncherRunning"
    When token "h" sends "GET /api/v1/worker-pools/{small}" through B
    Then the answer's workersCount is '1'
    When the worker chart release "beta" is uninstalled
    Then the worker chart release "beta" left only the worker CRDs

  @op:workerPoolManaged @case:failure
  Scenario Outline: Values the chart refuses fail the install before anything is created
    Then the worker chart install of "bad" in "home" with the values '<values>' is refused naming "<refusal>"

    Examples:
      | values                                                                  | refusal                                         |
      | {"privileged":true}                                                     | 'privileged'                                    |
      | {"controller":{"securityContext":{"privileged":true}}}                  | 'securityContext'                               |
      | {"controller":{"image":{"digest":"latest"}}}                            | at '/controller/image/digest'                   |
      | {"controller":{"fips140":"off"}}                                        | at '/controller/fips140'                        |
      | {"initializationTimeoutSeconds":120}                                    | at '/initializationTimeoutSeconds'              |
      | {"watchNamespaces":["{other}"]}                                         | is in a namespace watchNamespaces does not name |
      | {"managementCredentialsRef":null}                                       | Managed pools need                              |
      | {"managementCredentialsRef":{"name":"absent"}}                          | absent does not exist                           |
      | {"managementCredentialsRef":{"secretKey":"missing"}}                    | has no missing key                              |
      | {"trust":{"caSecretRef":{"name":"trust","key":"absent.pem"}}}           | has no absent.pem key                           |

  @op:workerPoolManaged @case:failure
  Scenario: A pool whose credential Secret does not exist fails the install
    Then the worker chart install of "bad" in "home" over a pool whose credential Secret does not exist is refused naming "credentialsRef: the Secret"
