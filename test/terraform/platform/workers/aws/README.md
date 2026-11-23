# EKS worker hosting acceptance

The AWS root of the [managed Kubernetes worker hosting contract](../../../../../docs/implementation/worker-hosting-contracts.md):
two stages, `cluster/` then `deployment/`, each with its own state, calling the
shared modules in [`deploy/terraform/modules/workers/aws`](../../../../../deploy/terraform/modules/workers/aws)
from this checkout. The workers attach to the platform scenario
([`platform/aws`](../../aws) or another cloud's), whose public HTTPS endpoint
they register with; this root deploys no server.

## What it creates

| Stage | Resources |
|---|---|
| `cluster/`, create mode | an EKS Auto Mode cluster (`general-purpose` node pool, the access API only, private and public API endpoints with `api_access.authorized_cidrs`), its cluster and node IAM roles under `stage_scope`'s path, prefix and permissions boundary, and, with `network.mode` create, a VPC over two availability zones with a public and a private subnet in each, an internet gateway, one NAT gateway with its Elastic IP, and route tables |
| `cluster/`, existing mode | nothing: it reads the cluster `existing_id` names back and requires it to be `ACTIVE`, at `kubernetes_version`, on Auto Mode and in `network.existing.vpc_id` |
| `deployment/` | the `infraweave-workers` chart from GHCR at the version and manifest digest `worker-artifacts.json` publishes, into a namespace the setup already created, with the WorkerPools of `pools` |

Auto Mode runs the nodes, VPC CNI, CoreDNS, kube-proxy, EBS driver and Pod
Identity agent itself, so the root declares no node group or add-on. The
controller and the run Pods hold no AWS identity: the controller speaks to the
Kubernetes API and the infraweave server, and the runs' Terraform is
provider-free, so the root creates no Pod Identity association. The workers
identity still allows them, for a later workload integration.

Every taggable resource carries `ownership.labels` and
`io.infraweave.lab.expires-at` through the provider's default tags; the
workers identity refuses a cluster without `io.infraweave.lab.owner`, and
deletes no VPC resource without it.

## Identity

Both stages use the caller's own credentials, which must resolve to the lab
account that [`landing-zone/aws`](../../landing-zone/aws) creates: the
provider refuses any other account, and the coordinator's preflight compares
the AWS CLI's identity with `cloud.identifiers.account_id`.

- In CI, `managed-cloud-workers.yml` signs in as `workers-identity/aws`'s role,
  whose grants are exactly what these stages need.
- Locally, a profile that assumes the lab account's administrator role
  through the operator profile:

  ```ini
  # ~/.aws/config
  [profile infraweave-lab-admin]
  role_arn = <landing-zone/aws's role_arn>
  source_profile = infraweave-lab
  region = eu-north-1
  ```

  ```sh
  export AWS_PROFILE=infraweave-lab-admin
  ```

`cluster.name` must start with `stage_scope.name_prefix` (`iw-workers-`), so
the roles it names fit the workers identity's grants. The creator's identity
gets cluster-admin through the access entry EKS creates for it, and the
deployment stage installs with that identity.

## Inputs

The coordinator's `--inputs aws=/absolute/path` file is private (0600),
names the account and holds no credential. The coordinator adds `ownership`,
`cloud` and, for `deployment/`, `cluster_stage`:

```json
{
  "cloud": {"provider": "aws", "identifiers": {"account_id": "<lab account>", "region": "eu-north-1"}},
  "cluster": {
    "cluster": {"mode": "create", "name": "iw-workers-lab", "kubernetes_version": "1.37"},
    "network": {"mode": "create",
                "create": {"vpc_cidr": "10.80.0.0/16", "availability_zones": ["eu-north-1a", "eu-north-1b"]},
                "api_access": {"private": false, "authorized_cidrs": ["0.0.0.0/0"]}},
    "stage_scope": {"name_prefix": "iw-workers-", "iam_path": "/infraweave-workers/",
                    "permissions_boundary": "<workers-identity/aws's stage_scope.permissions_boundary>"}
  },
  "deployment": {
    "cluster": {"mode": "create", "name": "iw-workers-lab", "cluster_id": "lab-aws", "kubernetes_version": "1.37"},
    "namespace": {"name": "iw-workers", "setup_owned": true},
    "installation": {"release_name": "workers", "crds": "owned"},
    "artifacts": "<worker-artifacts.json's revision, controller_image, bootstrap_image, runner_images and chart>",
    "module_revision": "<this checkout's full commit>",
    "server": {"endpoint": "<platform output's public_url>", "tls": {"system_roots": true}},
    "management_credentials_ref": {"name": "management", "id_key": "id", "secret_key": "secret"},
    "pools": [
      {"resource_name": "build", "mode": "Managed", "account": "<account ULID>", "space": "<space ULID>",
       "capacity": 2, "credentials_ref": "build-credentials", "settings": {"name": "EKS build"}},
      {"resource_name": "deploy", "mode": "Managed", "account": "<account ULID>", "space": "<space ULID>",
       "capacity": 2, "credentials_ref": "deploy-credentials", "settings": {"name": "EKS deploy"}}
    ],
    "resources": {
      "controller": {"requests": {"cpu": "100m", "memory": "256Mi", "ephemeral-storage": "1Gi"},
                     "limits": {"cpu": "2", "memory": "1Gi", "ephemeral-storage": "13Gi"}},
      "runner": {"requests": {"cpu": "500m", "memory": "1Gi", "ephemeral-storage": "12Gi"},
                 "limits": {"cpu": "2", "memory": "4Gi", "ephemeral-storage": "12Gi"}},
      "storage": {"run_size": "12Gi", "controller_cache": "12Gi"}
    },
    "timeouts": {"readiness_seconds": 900}
  }
}
```

The public endpoint must admit wherever Terraform and the coordinator run: a
GitHub-hosted runner has no fixed address, so CI needs `0.0.0.0/0`, and the
access API still authorizes every request. A local run can narrow it to the
operator's address. `artifacts` is the release's `worker-artifacts.json`
(`gh release download v<version> --pattern worker-artifacts.json`, or the
`worker-artifacts` artifact of a dispatch on `main`); the GHCR packages must
be public, because the stage resolves the chart's version to its digest
anonymously and refuses a mismatch. Verify the signatures first, as the
[chart's README](../../../../../deploy/helm/infraweave-workers/README.md)
shows.

## Setup between the stages

The deployment stage consumes a namespace and Secrets that already exist;
it never reads Secret data or generates a key. After `cluster/`, the setup
owner creates them from private files without printing them:

```sh
aws eks update-kubeconfig --name iw-workers-lab --region eu-north-1 --kubeconfig "$private/kubeconfig"
export KUBECONFIG="$private/kubeconfig"
kubectl create namespace iw-workers
kubectl label namespace iw-workers io.infraweave.lab.owner="$owner"
# The worker-pool-controller API key, created through the platform's API.
kubectl -n iw-workers create secret generic management \
  --from-file=id="$private/management-id" --from-file=secret="$private/management-secret"
# One P-256 private key per Managed pool; the controller registers its public half.
kubectl -n iw-workers create secret generic build-credentials --from-file=privateKey="$private/build.pem"
kubectl -n iw-workers create secret generic deploy-credentials --from-file=privateKey="$private/deploy.pem"
```

## Apply, run and destroy

The coordinator runs both stages under one owner:

```sh
python3 test/terraform/lab/labctl.py run --batch workers-aws --fips \
  --helper /private/tmp/infraweave-lab --directory /private/tmp/new-eks-batch \
  --inputs aws=/private/tmp/aws.json
```

By hand, each stage takes the same sequence with its own private paths:

```sh
terraform -chdir=test/terraform/platform/workers/aws/cluster init -input=false -lockfile=readonly \
  -backend-config=path="$private/cluster.tfstate"
terraform -chdir=test/terraform/platform/workers/aws/cluster plan -input=false \
  -var-file="$private/cluster.tfvars.json" -out="$private/cluster.tfplan"
terraform -chdir=test/terraform/platform/workers/aws/cluster apply -input=false "$private/cluster.tfplan"
terraform -chdir=test/terraform/platform/workers/aws/cluster output -json cluster > "$private/cluster.json"
```

The deployment stage's variables add `cluster_stage` from that output. An
EKS Auto Mode cluster takes about 10 to 15 minutes to create; the chart's
first install waits while Auto Mode starts a node for the controller.

Readback after apply:

```sh
aws eks describe-cluster --name iw-workers-lab --region eu-north-1 --query 'cluster.[status,version,computeConfig.enabled]'
kubectl -n iw-workers get workerpools,workers,pods -o wide
kubectl -n iw-workers get workerpools -o jsonpath='{range .items[*]}{.metadata.name} {.status.poolID} {.status.registered}{"\n"}{end}'
```

Destroy runs in the reverse order. The chart's uninstall Job deletes the
WorkerPools and waits while the controller ends their runs and deregisters
their workers, so the deployment stage goes first, then the setup's Secrets
and namespace, then the cluster stage:

```sh
terraform -chdir=test/terraform/platform/workers/aws/deployment destroy -input=false -var-file="$private/deployment.tfvars.json"
kubectl delete namespace iw-workers
terraform -chdir=test/terraform/platform/workers/aws/cluster destroy -input=false -var-file="$private/cluster.tfvars.json"
aws resourcegroupstaggingapi get-resources --region eu-north-1 --tag-filters Key=io.infraweave.lab.owner,Values="$owner"
aws eks describe-cluster --name iw-workers-lab --region eu-north-1   # ResourceNotFoundException
```

Absence needs both the empty states and that readback; the coordinator's
`down` performs it. Deleting the cluster removes Auto Mode's instances and
the CRDs with it. Existing mode destroys only the release: the borrowed
cluster, network and CRDs survive.

## Cost

While applied, in `eu-north-1`: the EKS cluster at $0.10 an hour, the NAT
gateway and its address at about $0.05, and the Auto Mode nodes the
controller and the runs need, EC2 plus Auto Mode's management fee, typically
one or two small instances at $0.05 to $0.20 an hour; roughly $0.20 to $0.35
an hour in all, before data transfer and NAT processing. Destroy both stages
when the batch ends.
