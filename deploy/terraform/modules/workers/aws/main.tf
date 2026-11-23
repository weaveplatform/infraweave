# The deployment half of EKS worker hosting: the infraweave-workers chart,
# installed from its published OCI package into a namespace the lab's setup
# already created, with the WorkerPools of var.pools. The caller configures
# the kubernetes and helm providers against the cluster stage's endpoint.
#
# No value here is a secret. Pool keys, tokens, the management API key and
# any CA bundle are Secrets that exist before this module runs and that it
# names only; Terraform never reads their data. The controller and the run
# Pods need no AWS identity, so no Pod Identity association is created: the
# controller talks to the Kubernetes API and the infraweave server, and the
# runs' Terraform is provider-free.

data "aws_eks_cluster" "target" {
  name = var.cluster_stage.name

  lifecycle {
    postcondition {
      condition     = self.arn == var.cluster_stage.id && self.status == "ACTIVE"
      error_message = "The cluster stage's cluster is not the ACTIVE cluster of this name."
    }
    postcondition {
      condition     = self.version == var.cluster.kubernetes_version
      error_message = "The cluster does not run cluster.kubernetes_version."
    }
    postcondition {
      condition     = length(self.compute_config) == 1 && self.compute_config[0].enabled
      error_message = "The cluster does not run EKS Auto Mode."
    }
  }
}

data "kubernetes_namespace_v1" "workers" {
  metadata {
    name = var.namespace.name
  }

  lifecycle {
    postcondition {
      condition     = self.metadata[0].uid != ""
      error_message = "namespace.name does not exist; the lab's setup creates it, and its Secrets, before this stage."
    }
  }
}

locals {
  managed = anytrue([for p in var.pools : p.mode == "Managed"])

  # ghcr.io/<owner>/<path> of oci://ghcr.io/<owner>/<path>
  chart_reference = trimprefix(var.artifacts.chart.source, "oci://")
  chart_registry  = split("/", local.chart_reference)[0]
  chart_path      = join("/", slice(split("/", local.chart_reference), 1, length(split("/", local.chart_reference))))
  chart_name      = basename(local.chart_reference)
  chart_sha256    = trimprefix(var.artifacts.chart.sha256, "sha256:")

  controller = split("@", var.artifacts.controller_image)
  bootstrap  = split("@", var.artifacts.bootstrap_image)
}

# A tag can move, so the version Helm pulls is resolved against the
# registry first and must name the published manifest digest. GHCR hands
# an anonymous pull token for a public package; it authorizes nothing else.
data "http" "chart_token" {
  url = "https://${local.chart_registry}/token?scope=repository:${local.chart_path}:pull&service=${local.chart_registry}"

  lifecycle {
    precondition {
      condition     = local.chart_registry == "ghcr.io"
      error_message = "artifacts.chart.source must be a public GHCR package, whose digest this module resolves anonymously."
    }
    postcondition {
      condition     = self.status_code == 200
      error_message = "GHCR refused an anonymous pull token for the chart; the package must be public."
    }
  }
}

data "http" "chart_manifest" {
  url    = "https://${local.chart_registry}/v2/${local.chart_path}/manifests/${var.artifacts.chart.version}"
  method = "HEAD"
  request_headers = {
    Accept        = "application/vnd.oci.image.manifest.v1+json"
    Authorization = "Bearer ${jsondecode(data.http.chart_token.response_body).token}"
  }

  lifecycle {
    postcondition {
      condition     = self.status_code == 200 && lookup(self.response_headers, "Docker-Content-Digest", "") == var.artifacts.chart.digest
      error_message = "The chart version on GHCR does not resolve to artifacts.chart.digest."
    }
  }
}

resource "helm_release" "workers" {
  name             = var.installation.release_name
  namespace        = var.namespace.name
  create_namespace = false
  repository       = "oci://${local.chart_registry}/${dirname(local.chart_path)}"
  chart            = local.chart_name
  version          = var.artifacts.chart.version
  skip_crds        = var.installation.crds == "existing"
  wait             = true
  timeout          = var.timeouts.readiness_seconds

  values = [yamlencode({
    clusterID       = var.cluster.cluster_id
    watchNamespaces = [var.namespace.name]
    controller = {
      image          = { repository = local.controller[0], digest = local.controller[1] }
      replicas       = var.controller_replicas
      resources      = var.resources.controller
      cacheSizeLimit = var.resources.storage.controller_cache
      nodeSelector   = var.resources.scheduling.node_selector
    }
    bootstrapImage = { repository = local.bootstrap[0], digest = local.bootstrap[1] }
    managementCredentialsRef = var.management_credentials_ref == null ? null : {
      name      = var.management_credentials_ref.name
      idKey     = var.management_credentials_ref.id_key
      secretKey = var.management_credentials_ref.secret_key
    }
    trust = { caSecretRef = var.server.tls.system_roots ? null : var.server.tls.ca_secret_ref }
    pools = [for p in var.pools : {
      name      = p.resource_name
      namespace = var.namespace.name
      # settings come first, so they can never replace the fields the
      # hosting contract fixes.
      spec = merge(
        p.settings,
        p.pool_id == null ? {} : { poolID = p.pool_id },
        p.account == null ? {} : { account = p.account },
        p.space == null ? {} : { space = p.space },
        {
          endpoint       = var.server.endpoint
          mode           = p.mode
          capacity       = p.capacity
          credentialsRef = { name = p.credentials_ref }
          pod = merge(
            {
              resources    = var.resources.runner
              storageSize  = var.resources.storage.run_size
              nodeSelector = var.resources.scheduling.node_selector
            },
            var.resources.policy_helper == null ? {} : { helperResources = var.resources.policy_helper },
          )
        },
      )
    }]
  })]

  lifecycle {
    precondition {
      condition     = local.managed == (var.management_credentials_ref != null)
      error_message = "management_credentials_ref is required when a pool is Managed and forbidden otherwise."
    }
    precondition {
      condition     = var.cluster_stage.owner == var.ownership.owner && var.cluster_stage.batch == var.ownership.batch
      error_message = "The cluster stage belongs to another owner or batch."
    }
  }

  depends_on = [data.http.chart_manifest, data.aws_eks_cluster.target]
}

# The WorkerPools as the API server holds them, for their UIDs.
data "kubernetes_resource" "pool" {
  for_each    = { for p in var.pools : p.resource_name => p }
  api_version = "workers.infraweave.io/v1alpha1"
  kind        = "WorkerPool"

  metadata {
    name      = each.key
    namespace = var.namespace.name
  }

  depends_on = [helm_release.workers]
}
