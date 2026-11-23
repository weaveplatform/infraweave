variable "ownership" {
  description = "Supplied by the lab coordinator: owner, batch, RFC 3339 expires_at and the io.infraweave.lab.* labels."
  type = object({
    owner      = string
    batch      = string
    expires_at = string
    labels     = map(string)
  })

  validation {
    condition     = lookup(var.ownership.labels, "io.infraweave.lab.owner", "") == var.ownership.owner
    error_message = "ownership.labels must carry io.infraweave.lab.owner with the owner's value."
  }
}

variable "cloud" {
  description = "Supplied by the lab coordinator from the inputs file: the lab account and region."
  type = object({
    provider    = string
    identifiers = object({ account_id = string, region = string })
  })
}

variable "cluster" {
  description = "mode (create or existing), name (starting with stage_scope.name_prefix), kubernetes_version and, in existing mode, existing_id, the cluster's ARN."
  type = object({
    mode               = string
    name               = string
    kubernetes_version = string
    existing_id        = optional(string)
  })
}

variable "network" {
  description = "The create or existing network branch and API access; see the module's variable."
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
}

variable "stage_scope" {
  description = "workers-identity/aws's stage_scope output."
  type = object({
    name_prefix          = string
    iam_path             = string
    permissions_boundary = string
  })
}
