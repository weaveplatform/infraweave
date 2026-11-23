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
- [test-terraform-layout](test-terraform-layout.drawio.svg): the `test/terraform` tree: the local lab and its `lab/services` root, the VCS labs and the per-cloud roots under `platform/`, with the landing zone every other cloud root applies into.
- [lab-platform-kubernetes](lab-platform-kubernetes.drawio.svg): the local platform lab and the k3s lab on a runner, with the batch lifecycle.
- [vcs-labs](vcs-labs.drawio.svg): the five VCS providers, their roots and the webhook paths through the tunnel or the Docker bridge gateway.
- [cloud-objectstore-kek](cloud-objectstore-kek.drawio.svg): the object store and KEK scenarios in the dedicated AWS account, Azure subscription and Google project that the landing zone creates.
- [cloud-platform](cloud-platform.drawio): the platform scenario, one infraweave platform behind public HTTPS in a cloud's dedicated account, with worker clusters on all three clouds attached; one page per cloud:
  - [AWS: ALB, ECS Fargate, RDS for PostgreSQL, S3](cloud-platform-aws.drawio.svg)
  - [Azure: Container Apps, PostgreSQL Flexible Server, Blob Storage](cloud-platform-azure.drawio.svg)
  - [GCP: HTTPS load balancer, Cloud Run, Cloud SQL, GCS](cloud-platform-google.drawio.svg)
- [managed-cloud-workers](managed-cloud-workers.drawio.svg): the dedicated account per cloud, its workers identity and the cluster and deployment stages.
- [multicloud-worker-batch](multicloud-worker-batch.drawio.svg): the concurrent three-cloud worker batch against one platform scenario, from preflight to verified cleanup.
- [infraweave-lab-test-architecture](infraweave-lab-test-architecture.drawio.svg): the batch lifecycle with its versioned inputs; lab-platform-kubernetes covers the same lab in more detail, so this one is a candidate for retirement.
