# The AWS identity of the managed cloud worker batches: one role that only a
# job in the repository's workers-cloud environment can assume, through
# GitHub's OIDC token, for up to four hours. It may create and destroy EKS
# clusters, node groups, access entries and Pod Identity associations, their
# VPC networking and log groups, and the IAM roles they need, in one region,
# and read the quotas and ownership tags preflight and cleanup check. No
# access key is created.
#
# Every role the role creates must sit under iam_path, start with
# name_prefix and carry the permissions boundary below, which caps what an
# EKS cluster, Auto Mode node or Pod Identity role can ever do; the role
# cannot remove or change that boundary, nor change itself.

data "aws_caller_identity" "current" {}

data "aws_partition" "current" {}

resource "aws_iam_openid_connect_provider" "github" {
  count          = var.github_oidc_provider_arn == null ? 1 : 0
  url            = "https://token.actions.githubusercontent.com"
  client_id_list = ["sts.amazonaws.com"]
}

locals {
  oidc_provider_arn = coalesce(var.github_oidc_provider_arn, one(aws_iam_openid_connect_provider.github[*].arn))

  account   = data.aws_caller_identity.current.account_id
  partition = data.aws_partition.current.partition
  iam       = "arn:${local.partition}:iam::${local.account}"
  eks       = "arn:${local.partition}:eks:${var.region}:${local.account}"

  # The CI role and the boundary share this name; the role is denied every
  # IAM write on it. Stage roles sit under iam_path, so their
  # ARNs never match it.
  self = "infraweave-workers"

  stage_roles    = "${local.iam}:role${var.iam_path}${var.name_prefix}*"
  stage_policies = "${local.iam}:policy${var.iam_path}${var.name_prefix}*"

  # lab.OWNER_LABEL in test/terraform/lab/labctl.py; cleanup's tag search
  # finds what carries it.
  owner_tag = "io.infraweave.lab.owner"

  # AWS managed policies a stage role may attach: the EKS cluster, Auto Mode
  # compute, networking, storage and load balancing, node and CNI policies,
  # and read-only image pulls.
  attachable_policies = [
    "arn:${local.partition}:iam::aws:policy/AmazonEKS*",
    "arn:${local.partition}:iam::aws:policy/AmazonEC2ContainerRegistryPullOnly",
    "arn:${local.partition}:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly",
    local.stage_policies,
  ]

  region = {
    test     = "StringEquals"
    variable = "aws:RequestedRegion"
    values   = [var.region]
  }
}

# The ceiling of every role a stage creates. It admits what the attachable
# EKS managed policies use, so attaching them works unchanged, and no IAM
# beyond instance profiles, service-linked roles and passing stage roles.
# ec2:* and elasticloadbalancing:* are broader than any one attached policy
# needs: Auto Mode's compute, networking, storage and load balancing
# policies together use most of both services, and they remain bounded by
# the attached policy.
data "aws_iam_policy_document" "boundary" {
  statement {
    sid = "ClusterAndNodeServices"
    actions = [
      "ec2:*",
      "elasticloadbalancing:*",
      "autoscaling:Describe*",
      "eks-auth:AssumeRoleForPodIdentity",
      "ecr:GetAuthorizationToken",
      "ecr:BatchCheckLayerAvailability",
      "ecr:BatchGetImage",
      "ecr:GetDownloadUrlForLayer",
      "kms:CreateGrant",
      "kms:DescribeKey",
      "ssm:GetParameter",
      "ssm:GetParameters",
      "logs:CreateLogGroup",
      "logs:CreateLogStream",
      "logs:DescribeLogGroups",
      "logs:DescribeLogStreams",
      "logs:PutLogEvents",
      "cloudwatch:PutMetricData",
      "route53:AssociateVPCWithHostedZone",
      "acm:DescribeCertificate",
      "acm:ListCertificates",
      "shield:GetSubscriptionState",
      "wafv2:GetWebACLForResource",
      "tag:GetResources",
      "iam:CreateServiceLinkedRole",
      "iam:GetInstanceProfile",
      "iam:ListInstanceProfiles",
      "iam:ListInstanceProfilesForRole",
      "iam:CreateInstanceProfile",
      "iam:DeleteInstanceProfile",
      "iam:TagInstanceProfile",
      "iam:AddRoleToInstanceProfile",
      "iam:RemoveRoleFromInstanceProfile",
    ]
    resources = ["*"]
  }

  statement {
    sid       = "PassStageRoles"
    actions   = ["iam:PassRole"]
    resources = [local.stage_roles]
  }
}

resource "aws_iam_policy" "boundary" {
  name        = "${local.self}-boundary"
  description = "The permissions boundary of every role the managed cloud worker stages create."
  policy      = data.aws_iam_policy_document.boundary.json
}

data "aws_iam_policy_document" "trust" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [local.oidc_provider_arn]
    }
    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:sub"
      values   = [var.subject]
    }
  }
}

# 14400 seconds matches role-duration-seconds in managed-cloud-workers.yml:
# an hour to provision, the scenarios, and an hour to clean up.
resource "aws_iam_role" "ci" {
  name                 = "${local.self}-ci"
  assume_role_policy   = data.aws_iam_policy_document.trust.json
  max_session_duration = 14400
}

# Preflight's identity, quota and inventory reads, cleanup's tag search and
# exact cluster lookups, and what Terraform reads while it plans.
data "aws_iam_policy_document" "inventory" {
  statement {
    sid = "ReadRegion"
    actions = [
      "ec2:Describe*",
      "eks:Describe*",
      "eks:List*",
      "logs:DescribeLogGroups",
      "tag:GetResources",
    ]
    resources = ["*"]
    condition {
      test     = local.region.test
      variable = local.region.variable
      values   = local.region.values
    }
  }

  # Running On-Demand Standard instance vCPUs and EKS clusters per Region,
  # the two quotas cloudlab.py's verify_quota reads.
  statement {
    sid     = "ReadQuotas"
    actions = ["servicequotas:GetServiceQuota"]
    resources = [
      "arn:${local.partition}:servicequotas:${var.region}:${local.account}:ec2/L-1216C47A",
      "arn:${local.partition}:servicequotas:${var.region}:${local.account}:eks/L-1194D53C",
    ]
  }

  # The public EKS and Bottlerocket AMI parameters.
  statement {
    sid       = "ReadPublicParameters"
    actions   = ["ssm:GetParameter", "ssm:GetParameters"]
    resources = ["arn:${local.partition}:ssm:${var.region}::parameter/aws/service/*"]
  }

  statement {
    sid = "ReadStageIdentities"
    actions = [
      "iam:GetRole",
      "iam:GetRolePolicy",
      "iam:ListRolePolicies",
      "iam:ListAttachedRolePolicies",
      "iam:ListInstanceProfilesForRole",
      "iam:ListRoleTags",
      "iam:GetPolicy",
      "iam:GetPolicyVersion",
      "iam:ListPolicyVersions",
      "iam:ListPolicyTags",
    ]
    resources = concat([local.stage_roles, aws_iam_policy.boundary.arn], local.attachable_policies)
  }

  statement {
    sid = "ManageStageLogGroups"
    actions = [
      "logs:CreateLogGroup",
      "logs:DeleteLogGroup",
      "logs:PutRetentionPolicy",
      "logs:DeleteRetentionPolicy",
      "logs:TagResource",
      "logs:UntagResource",
      "logs:ListTagsForResource",
    ]
    resources = ["arn:${local.partition}:logs:${var.region}:${local.account}:log-group:/aws/eks/${var.name_prefix}*"]
  }
}

data "aws_iam_policy_document" "clusters" {
  # A cluster created without the owner tag would escape cleanup's search.
  statement {
    sid       = "CreateOwnedClusters"
    actions   = ["eks:CreateCluster"]
    resources = ["${local.eks}:cluster/${var.name_prefix}*"]
    condition {
      test     = "Null"
      variable = "aws:RequestTag/${local.owner_tag}"
      values   = ["false"]
    }
  }

  statement {
    sid = "ManageStageClusters"
    actions = [
      "eks:DeleteCluster",
      "eks:UpdateClusterConfig",
      "eks:UpdateClusterVersion",
      "eks:TagResource",
      "eks:UntagResource",
      "eks:CreateNodegroup",
      "eks:DeleteNodegroup",
      "eks:UpdateNodegroupConfig",
      "eks:UpdateNodegroupVersion",
      "eks:CreateAddon",
      "eks:DeleteAddon",
      "eks:UpdateAddon",
      "eks:CreateAccessEntry",
      "eks:DeleteAccessEntry",
      "eks:UpdateAccessEntry",
      "eks:AssociateAccessPolicy",
      "eks:DisassociateAccessPolicy",
      "eks:CreatePodIdentityAssociation",
      "eks:DeletePodIdentityAssociation",
      "eks:UpdatePodIdentityAssociation",
    ]
    # An ARN pattern's * also matches slashes, so each covers every
    # node group, add-on, access entry and association of a stage cluster.
    resources = [
      for kind in ["cluster", "nodegroup", "addon", "access-entry", "podidentityassociation"] :
      "${local.eks}:${kind}/${var.name_prefix}*"
    ]
  }

  dynamic "statement" {
    for_each = length(var.existing_cluster_names) > 0 ? [1] : []
    content {
      sid = "ManageExistingClusterPodIdentity"
      actions = [
        "eks:CreatePodIdentityAssociation",
        "eks:DeletePodIdentityAssociation",
        "eks:UpdatePodIdentityAssociation",
        "eks:TagResource",
        "eks:UntagResource",
      ]
      resources = flatten([
        for name in var.existing_cluster_names :
        ["${local.eks}:cluster/${name}", "${local.eks}:podidentityassociation/${name}/*"]
      ])
    }
  }
}

data "aws_iam_policy_document" "identities" {
  # iam:PermissionsBoundary is the boundary a new role is given and, for
  # the policy actions, the one an existing role already has.
  statement {
    sid = "ManageBoundedStageRoles"
    actions = [
      "iam:CreateRole",
      "iam:PutRolePermissionsBoundary",
      "iam:PutRolePolicy",
      "iam:DeleteRolePolicy",
      "iam:DetachRolePolicy",
    ]
    resources = [local.stage_roles]
    condition {
      test     = "StringEquals"
      variable = "iam:PermissionsBoundary"
      values   = [aws_iam_policy.boundary.arn]
    }
  }

  statement {
    sid       = "AttachApprovedPolicies"
    actions   = ["iam:AttachRolePolicy"]
    resources = [local.stage_roles]
    condition {
      test     = "StringEquals"
      variable = "iam:PermissionsBoundary"
      values   = [aws_iam_policy.boundary.arn]
    }
    condition {
      test     = "ArnLike"
      variable = "iam:PolicyARN"
      values   = local.attachable_policies
    }
  }

  # These actions take no boundary condition key; the boundary still caps
  # whatever a trust policy change admits.
  statement {
    sid = "MaintainStageRoles"
    actions = [
      "iam:DeleteRole",
      "iam:TagRole",
      "iam:UntagRole",
      "iam:UpdateRole",
      "iam:UpdateRoleDescription",
      "iam:UpdateAssumeRolePolicy",
    ]
    resources = [local.stage_roles]
  }

  statement {
    sid = "ManageStagePolicies"
    actions = [
      "iam:CreatePolicy",
      "iam:DeletePolicy",
      "iam:CreatePolicyVersion",
      "iam:DeletePolicyVersion",
      "iam:TagPolicy",
      "iam:UntagPolicy",
    ]
    resources = [local.stage_policies]
  }

  # EKS takes the cluster and node roles; Pod Identity takes workload roles.
  statement {
    sid       = "PassStageRoles"
    actions   = ["iam:PassRole"]
    resources = [local.stage_roles]
    condition {
      test     = "StringEquals"
      variable = "iam:PassedToService"
      values   = ["eks.amazonaws.com", "pods.eks.amazonaws.com"]
    }
  }

  statement {
    sid       = "CreateEKSServiceLinkedRoles"
    actions   = ["iam:CreateServiceLinkedRole"]
    resources = ["${local.iam}:role/aws-service-role/*"]
    condition {
      test     = "StringEquals"
      variable = "iam:AWSServiceName"
      values = [
        "eks.amazonaws.com",
        "eks-nodegroup.amazonaws.com",
        "autoscaling.amazonaws.com",
        "elasticloadbalancing.amazonaws.com",
      ]
    }
  }

  statement {
    sid       = "KeepBoundaries"
    effect    = "Deny"
    actions   = ["iam:DeleteRolePermissionsBoundary"]
    resources = ["*"]
  }

  statement {
    sid         = "ProtectSelf"
    effect      = "Deny"
    not_actions = ["iam:Get*", "iam:List*"]
    resources   = ["${local.iam}:role/${local.self}-*", "${local.iam}:policy/${local.self}-*"]
  }
}

# EC2 authorizes a rule, route or association against resources that are
# not yet tagged, such as a new security group rule, so creating and
# changing networking is open across the region: broader than ideal, and
# harmless to anything not created here. Deleting, detaching and releasing
# need the owner tag, so another workload's network is never torn down.
data "aws_iam_policy_document" "network" {
  statement {
    sid = "BuildNetworks"
    actions = [
      "ec2:CreateVpc",
      "ec2:ModifyVpcAttribute",
      "ec2:CreateSubnet",
      "ec2:ModifySubnetAttribute",
      "ec2:CreateInternetGateway",
      "ec2:AttachInternetGateway",
      "ec2:CreateRouteTable",
      "ec2:CreateRoute",
      "ec2:ReplaceRoute",
      "ec2:AssociateRouteTable",
      "ec2:AllocateAddress",
      "ec2:CreateNatGateway",
      "ec2:CreateSecurityGroup",
      "ec2:AuthorizeSecurityGroupIngress",
      "ec2:AuthorizeSecurityGroupEgress",
      "ec2:RevokeSecurityGroupIngress",
      "ec2:RevokeSecurityGroupEgress",
      "ec2:ModifySecurityGroupRules",
      "ec2:UpdateSecurityGroupRuleDescriptionsIngress",
      "ec2:UpdateSecurityGroupRuleDescriptionsEgress",
      "ec2:CreateVpcEndpoint",
      "ec2:ModifyVpcEndpoint",
    ]
    resources = ["*"]
    condition {
      test     = local.region.test
      variable = local.region.variable
      values   = local.region.values
    }
  }

  statement {
    sid = "TearDownOwnedNetworks"
    actions = [
      "ec2:DeleteVpc",
      "ec2:DeleteSubnet",
      "ec2:DetachInternetGateway",
      "ec2:DeleteInternetGateway",
      "ec2:DeleteRoute",
      "ec2:DisassociateRouteTable",
      "ec2:DeleteRouteTable",
      "ec2:DeleteNatGateway",
      "ec2:DisassociateAddress",
      "ec2:ReleaseAddress",
      "ec2:DeleteSecurityGroup",
      "ec2:DeleteVpcEndpoints",
      "ec2:DeleteTags",
    ]
    resources = ["*"]
    condition {
      test     = local.region.test
      variable = local.region.variable
      values   = local.region.values
    }
    condition {
      test     = "Null"
      variable = "aws:ResourceTag/${local.owner_tag}"
      values   = ["false"]
    }
  }

  # Tags arrive with the create call, or later on a resource that already
  # carries the owner tag; tagging anything else would let a stage adopt it.
  statement {
    sid       = "TagOnCreate"
    actions   = ["ec2:CreateTags"]
    resources = ["*"]
    condition {
      test     = "Null"
      variable = "ec2:CreateAction"
      values   = ["false"]
    }
  }

  statement {
    sid       = "RetagOwnedResources"
    actions   = ["ec2:CreateTags"]
    resources = ["*"]
    condition {
      test     = "Null"
      variable = "aws:ResourceTag/${local.owner_tag}"
      values   = ["false"]
    }
  }
}

# Inline, as one policy per concern: together they stay under IAM's
# 10,240-character limit on a role's inline policies.
locals {
  policies = {
    inventory  = data.aws_iam_policy_document.inventory.json
    clusters   = data.aws_iam_policy_document.clusters.json
    identities = data.aws_iam_policy_document.identities.json
    network    = data.aws_iam_policy_document.network.json
  }
}

resource "aws_iam_role_policy" "ci" {
  for_each = local.policies
  name     = each.key
  role     = aws_iam_role.ci.id
  policy   = each.value
}
