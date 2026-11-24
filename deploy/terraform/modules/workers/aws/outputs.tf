# The hosting contract's common output, which the lab coordinator's
# validate_hosting checks field by field before any scenario.
output "hosting" {
  value = {
    schema     = 1
    kind       = "worker-hosting"
    owner      = var.ownership.owner
    batch      = var.ownership.batch
    expires_at = var.ownership.expires_at
    cloud      = var.cloud
    cluster = {
      id                 = var.cluster_stage.id
      name               = var.cluster_stage.name
      cluster_id         = var.cluster.cluster_id
      kubernetes_version = "${data.aws_eks_cluster.target.version} (${data.aws_eks_cluster.target.platform_version})"
      managed_profile    = "eks-auto-mode"
      owned              = var.cluster_stage.owned
      network_ids        = concat([data.aws_eks_cluster.target.vpc_config[0].vpc_id], tolist(data.aws_eks_cluster.target.vpc_config[0].subnet_ids))
    }
    namespace = {
      name  = var.namespace.name
      uid   = data.kubernetes_namespace_v1.workers.metadata[0].uid
      owned = var.namespace.setup_owned
    }
    installation = {
      release_name  = helm_release.workers.name
      chart_version = helm_release.workers.metadata.version
      chart_sha256  = local.chart_sha256
      # The chart names it <release>-worker-controller, cut to 63 characters.
      controller_service_account = trimsuffix(substr("${var.installation.release_name}-worker-controller", 0, 63), "-")
      # Run Pods use the namespace's default service account with no
      # token mounted, so they hold no Kubernetes API right.
      run_service_account = "default"
      crds_owned          = var.installation.crds == "owned"
    }
    artifacts = {
      module_revision  = var.module_revision
      controller_image = var.artifacts.controller_image
      bootstrap_image  = var.artifacts.bootstrap_image
      runner_images    = var.artifacts.runner_images
    }
    pools = [for p in var.pools : {
      resource_name     = p.resource_name
      resource_uid      = data.kubernetes_resource.pool[p.resource_name].object.metadata.uid
      mode              = p.mode
      requested_pool_id = p.pool_id
      capacity          = p.capacity
      credentials_ref   = p.credentials_ref
    }]
    resources = var.resources
    inventory = local.inventory
  }
}

# Kubernetes objects are scoped to the cluster's ARN. Helm never deletes
# CRDs, so owned ones go with the cluster in create mode and otherwise
# need the installation's cleanup owner.
locals {
  inventory = [for e in concat(
    [
      { kind = "helm:release", id = "${var.namespace.name}/${helm_release.workers.name}", lifecycle = "deployment", owned = true },
      { kind = "kubernetes:namespace", id = data.kubernetes_namespace_v1.workers.metadata[0].uid, lifecycle = var.namespace.setup_owned ? "setup" : "external", owned = var.namespace.setup_owned },
    ],
    [for p in var.pools : {
      kind      = "kubernetes:workerpool"
      id        = data.kubernetes_resource.pool[p.resource_name].object.metadata.uid
      lifecycle = "deployment"
      owned     = true
    }],
    [for crd in ["workerpools", "workers"] : {
      kind      = "kubernetes:customresourcedefinition"
      id        = "${crd}.workers.infraweave.io"
      lifecycle = var.installation.crds == "owned" ? "deployment" : "external"
      owned     = var.installation.crds == "owned"
    }],
  ) : merge(e, { scope = var.cluster_stage.id, owner = var.ownership.owner })]
}
