# Diagrams

Each diagram is a draw.io source (`.drawio`) with its SVG export (`.drawio.svg`), which embeds the diagram and opens in draw.io for editing.

## Product

- [account-deletion](account-deletion.drawio.svg): account and organization deletion: the event each domain consumes to purge its rows, and the nightly check.
- [api-surfaces](api-surfaces.drawio.svg): the four surfaces one listener serves, each with its own credentials and contract.
- [run-reports](run-reports.drawio.svg): a claimed run's phase reports and uploads, with bytes going to the object store through signed URLs.
- [run-specification](run-specification.drawio.svg): a run's specification at handover, with its secrets sealed to the pool's key.
- [stack-worker-pools](stack-worker-pools.drawio.svg): which private worker pools a stack can name, and where default runner images live.

## Test infrastructure

- [test-infrastructure-overview](test-infrastructure-overview.drawio.svg): every test scenario, from trigger and GitHub environment through its Terraform roots to its local or cloud target.
- [lab-platform-kubernetes](lab-platform-kubernetes.drawio.svg): the local platform lab and the k3s lab on a runner, with the batch lifecycle.
- [vcs-labs](vcs-labs.drawio.svg): the five VCS providers, their roots and the webhook paths through the tunnel or the Docker bridge gateway.
- [cloud-objectstore-kek](cloud-objectstore-kek.drawio.svg): the object store and KEK scenarios in the current AWS account, Azure subscription and Google project.
- [managed-cloud-workers](managed-cloud-workers.drawio.svg): the dedicated worker account per cloud, its identity and the cluster and deployment stages.
- [worker-server-endpoint-options](worker-server-endpoint-options.drawio): **proposal — choose one** of how worker Pods reach the server; one page per option:
  - [A: tunnel from the runner](worker-server-endpoint-options-a.drawio.svg)
  - [B: hub server in one cloud](worker-server-endpoint-options-b.drawio.svg)
  - [C: per-cloud ingress relay](worker-server-endpoint-options-c.drawio.svg)
- [multicloud-worker-batch](multicloud-worker-batch.drawio.svg): the concurrent three-cloud worker batch, from preflight to verified cleanup.
- [infraweave-lab-test-architecture](infraweave-lab-test-architecture.drawio.svg): the batch lifecycle with its versioned inputs; lab-platform-kubernetes covers the same lab in more detail, so this one is a candidate for retirement.
