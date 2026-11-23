# The infrastructure half of EKS worker hosting: an EKS Auto Mode cluster,
# its network and its two IAM roles in create mode, or a verified readback
# of an explicit existing cluster, which creates nothing. Kubernetes is not
# touched here, so the deployment module's providers initialize only once
# the cluster's endpoint exists.
#
# Every role sits under stage_scope's path, starts with its prefix and
# carries its permissions boundary, the conditions under which the
# workers-cloud identity may create a role at all.

data "aws_caller_identity" "current" {}

data "aws_partition" "current" {}

locals {
  create    = var.cluster.mode == "create"
  partition = data.aws_partition.current.partition
  managed   = "arn:${local.partition}:iam::aws:policy"

  # The managed policies EKS Auto Mode requires of its cluster and node
  # roles.
  # ref: https://docs.aws.amazon.com/eks/latest/userguide/automode-get-started-cli.html
  cluster_policies = [
    "AmazonEKSClusterPolicy",
    "AmazonEKSComputePolicy",
    "AmazonEKSBlockStoragePolicy",
    "AmazonEKSLoadBalancingPolicy",
    "AmazonEKSNetworkingPolicy",
  ]
  node_policies = [
    "AmazonEKSWorkerNodeMinimalPolicy",
    "AmazonEC2ContainerRegistryPullOnly",
  ]
}

data "aws_iam_policy_document" "cluster_trust" {
  statement {
    actions = ["sts:AssumeRole", "sts:TagSession"]
    principals {
      type        = "Service"
      identifiers = ["eks.amazonaws.com"]
    }
  }
}

data "aws_iam_policy_document" "node_trust" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "cluster" {
  count                = local.create ? 1 : 0
  name                 = "${var.cluster.name}-cluster"
  path                 = var.stage_scope.iam_path
  permissions_boundary = var.stage_scope.permissions_boundary
  assume_role_policy   = data.aws_iam_policy_document.cluster_trust.json

  lifecycle {
    precondition {
      condition     = startswith(var.cluster.name, var.stage_scope.name_prefix)
      error_message = "cluster.name must start with stage_scope.name_prefix, the only names the workers identity may create."
    }
    precondition {
      condition     = data.aws_caller_identity.current.account_id == var.cloud.identifiers.account_id
      error_message = "The caller's credentials resolve to an account other than cloud.identifiers.account_id."
    }
    precondition {
      condition     = var.network.mode == "create" || length(var.network.existing.subnet_ids) >= 2
      error_message = "An existing network must name at least two subnets in different availability zones."
    }
  }
}

resource "aws_iam_role_policy_attachment" "cluster" {
  for_each   = toset(local.create ? local.cluster_policies : [])
  role       = aws_iam_role.cluster[0].name
  policy_arn = "${local.managed}/${each.key}"
}

resource "aws_iam_role" "node" {
  count                = local.create ? 1 : 0
  name                 = "${var.cluster.name}-node"
  path                 = var.stage_scope.iam_path
  permissions_boundary = var.stage_scope.permissions_boundary
  assume_role_policy   = data.aws_iam_policy_document.node_trust.json
}

resource "aws_iam_role_policy_attachment" "node" {
  for_each   = toset(local.create ? local.node_policies : [])
  role       = aws_iam_role.node[0].name
  policy_arn = "${local.managed}/${each.key}"
}

# Auto Mode: EKS runs the nodes, the VPC CNI, kube-proxy, CoreDNS, the EBS
# driver and the Pod Identity agent itself, so no node group or add-on is
# declared. The access API alone authorizes; the caller that creates the
# cluster gets cluster-admin through its access entry, which the
# deployment stage installs with.
resource "aws_eks_cluster" "workers" {
  count    = local.create ? 1 : 0
  name     = var.cluster.name
  version  = var.cluster.kubernetes_version
  role_arn = aws_iam_role.cluster[0].arn

  bootstrap_self_managed_addons = false

  access_config {
    authentication_mode                         = "API"
    bootstrap_cluster_creator_admin_permissions = true
  }

  compute_config {
    enabled       = true
    node_pools    = ["general-purpose"]
    node_role_arn = aws_iam_role.node[0].arn
  }

  kubernetes_network_config {
    elastic_load_balancing {
      enabled = true
    }
  }

  storage_config {
    block_storage {
      enabled = true
    }
  }

  vpc_config {
    subnet_ids              = local.subnet_ids
    security_group_ids      = local.security_group_ids
    endpoint_private_access = true
    endpoint_public_access  = !var.network.api_access.private
    public_access_cidrs     = var.network.api_access.private ? null : var.network.api_access.authorized_cidrs
  }

  # The routes must exist before nodes start, or they cannot pull images;
  # the policies before the cluster, or EKS cannot use its role.
  depends_on = [
    aws_iam_role_policy_attachment.cluster,
    aws_iam_role_policy_attachment.node,
    aws_route_table_association.private,
    aws_route.private_nat,
  ]
}

# Existing mode borrows the cluster and verifies the profile this module
# supports instead of assuming it.
data "aws_eks_cluster" "existing" {
  count = local.create ? 0 : 1
  name  = var.cluster.name

  lifecycle {
    postcondition {
      condition     = lower(self.arn) == lower(var.cluster.existing_id)
      error_message = "The cluster named cluster.name is not cluster.existing_id."
    }
    postcondition {
      condition     = self.version == var.cluster.kubernetes_version && self.status == "ACTIVE"
      error_message = "The existing cluster is not ACTIVE at cluster.kubernetes_version."
    }
    postcondition {
      condition     = length(self.compute_config) == 1 && self.compute_config[0].enabled
      error_message = "The existing cluster does not run EKS Auto Mode, the only existing profile this module verifies."
    }
    postcondition {
      condition     = var.network.mode == "existing" && self.vpc_config[0].vpc_id == var.network.existing.vpc_id
      error_message = "An existing cluster needs network.mode existing naming its own VPC."
    }
  }
}

locals {
  arn      = local.create ? aws_eks_cluster.workers[0].arn : data.aws_eks_cluster.existing[0].arn
  endpoint = local.create ? aws_eks_cluster.workers[0].endpoint : data.aws_eks_cluster.existing[0].endpoint
  ca       = local.create ? aws_eks_cluster.workers[0].certificate_authority[0].data : data.aws_eks_cluster.existing[0].certificate_authority[0].data
  version  = local.create ? aws_eks_cluster.workers[0].version : data.aws_eks_cluster.existing[0].version
  platform = local.create ? aws_eks_cluster.workers[0].platform_version : data.aws_eks_cluster.existing[0].platform_version
}
