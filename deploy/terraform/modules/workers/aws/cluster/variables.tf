variable "ownership" {
  description = "The lab owner, batch, RFC 3339 expiry and labels. Every resource carries the labels as tags through the caller's provider default_tags; the owner names nothing here, so two owners never collide through it."
  type = object({
    owner      = string
    batch      = string
    expires_at = string
    labels     = map(string)
  })
}

variable "cloud" {
  description = "The explicit AWS account and region the cluster lives in. The caller's provider must resolve to them; this module only checks."
  type = object({
    provider    = string
    identifiers = object({ account_id = string, region = string })
  })

  validation {
    condition     = var.cloud.provider == "aws"
    error_message = "cloud.provider must be aws."
  }
}

variable "cluster" {
  description = "create builds an EKS Auto Mode cluster named name; existing reads the cluster existing_id names back and creates nothing. kubernetes_version is the exact EKS minor either way."
  type = object({
    mode               = string
    name               = string
    kubernetes_version = string
    existing_id        = optional(string)
  })

  validation {
    condition     = contains(["create", "existing"], var.cluster.mode)
    error_message = "cluster.mode must be create or existing."
  }
  validation {
    # The role names append -cluster and -node, which IAM caps at 64.
    condition     = can(regex("^[a-z][a-z0-9-]{2,39}$", var.cluster.name))
    error_message = "cluster.name must be 3 to 40 lowercase letters, digits or hyphens, starting with a letter."
  }
  validation {
    condition     = (var.cluster.mode == "existing") == (var.cluster.existing_id != null)
    error_message = "cluster.existing_id is required for, and only for, existing mode."
  }
  validation {
    condition     = can(regex("^1\\.[0-9]+$", var.cluster.kubernetes_version))
    error_message = "cluster.kubernetes_version must be an EKS minor such as 1.37."
  }
}

variable "network" {
  description = "Exactly one of create (a VPC of vpc_cidr over two availability_zones, with one NAT gateway) and existing (borrowed IDs). api_access states who reaches the public API endpoint; the private endpoint is always on."
  type = object({
    mode = string
    create = optional(object({
      vpc_cidr           = string
      availability_zones = list(string)
    }))
    existing = optional(object({
      vpc_id             = string
      subnet_ids         = list(string)
      security_group_ids = list(string)
    }))
    api_access = object({
      private          = bool
      authorized_cidrs = list(string)
    })
  })

  validation {
    condition = (
      contains(["create", "existing"], var.network.mode) &&
      (var.network.mode == "create") == (var.network.create != null) &&
      (var.network.mode == "existing") == (var.network.existing != null)
    )
    error_message = "network.mode must be create or existing, with exactly the matching branch set."
  }
  validation {
    condition     = var.network.create == null || (can(cidrnetmask(var.network.create.vpc_cidr)) && tonumber(split("/", var.network.create.vpc_cidr)[1]) <= 20 && length(var.network.create.availability_zones) == 2)
    error_message = "network.create needs a /20 or larger vpc_cidr and exactly two availability zones."
  }
  validation {
    condition     = var.network.api_access.private || length(var.network.api_access.authorized_cidrs) > 0
    error_message = "A public API endpoint needs explicit authorized_cidrs; none is inferred."
  }
}

variable "stage_scope" {
  description = "workers-identity/aws's stage_scope output: the name prefix, IAM path and permissions boundary every role here must carry."
  type = object({
    name_prefix          = string
    iam_path             = string
    permissions_boundary = string
  })
}
