variable "project_id" {
  description = "The dedicated lab project the platform lives in: landing-zone/google's project_id output."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.project_id))
    error_message = "project_id must be 6 to 30 lowercase letters, digits or hyphens, starting with a letter and not ending with a hyphen."
  }
}

variable "region" {
  description = "The region of the Cloud Run service, Cloud SQL instance, subnet, bucket, key ring and Artifact Registry repository."
  type        = string
  default     = "us-central1"

  validation {
    condition     = can(regex("^[a-z]+-[a-z]+[0-9]$", var.region))
    error_message = "region must be a Google Cloud region, such as us-central1."
  }
}

variable "ownership" {
  description = "The batch that owns this platform, as cloudlab.py supplies it to every stage: owner, batch, an RFC 3339 expires_at, and the io.infraweave.lab.* labels, which every resource that takes labels carries with dots and other characters Google labels refuse turned into underscores."
  type = object({
    owner      = string
    batch      = string
    expires_at = string
    labels     = map(string)
  })

  # Google label values admit only lowercase letters, digits, underscores
  # and hyphens, and the owner search compares the value exactly.
  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9_-]{0,62}$", var.ownership.owner)) && var.ownership.batch != ""
    error_message = "ownership.owner must be 1 to 63 lowercase letters, digits, underscores or hyphens, and ownership.batch must be set."
  }

  validation {
    condition     = can(timeadd(var.ownership.expires_at, "0s"))
    error_message = "ownership.expires_at must be an RFC 3339 timestamp."
  }

  # Cleanup finds what is left by this label, so a label naming another
  # owner would hide this platform's resources from it.
  validation {
    condition     = lookup(var.ownership.labels, "io.infraweave.lab.owner", null) == var.ownership.owner
    error_message = "ownership.labels must carry io.infraweave.lab.owner with ownership.owner's value."
  }
}

variable "image" {
  description = "The infraweave server image on GHCR, pinned by digest (ghcr.io/<path>@sha256:<digest>). Cloud Run pulls it through this root's Artifact Registry remote repository. Its entrypoint is infraweave all; the service passes it only --config."
  type        = string

  validation {
    condition     = can(regex("^ghcr\\.io/[a-z0-9]+([._-][a-z0-9]+)*(/[a-z0-9]+([._-][a-z0-9]+)*)*@sha256:[0-9a-f]{64}$", var.image))
    error_message = "image must be a GHCR image pinned by digest: ghcr.io/<path>@sha256:<64 hex digits>, with no tag."
  }
}

variable "domain_name" {
  description = "The lab domain the platform answers at, server.public_url's host. Its Google-managed certificate provisions only once the domain's A record points at the load balancer's address."
  type        = string

  validation {
    condition     = can(regex("^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\\.)+[a-z]{2,63}$", var.domain_name))
    error_message = "domain_name must be a lower-case DNS name."
  }
}

variable "dns_managed_zone" {
  description = "The Cloud DNS managed zone, in the same project, that holds domain_name; the root creates the A record there. Null leaves the record to whoever controls the domain, who points it at the load_balancer_ip in the platform output."
  type        = string
  default     = null
}

variable "operator_keys" {
  description = "auth.operator.keys: the operator keys whose secrets the acceptance runner holds, as a ULID and the lower-case hex SHA-256 of the secret. Empty starts a platform no operator can act on."
  type = list(object({
    id            = string
    secret_sha256 = string
  }))
  default = []

  validation {
    condition = alltrue([for k in var.operator_keys :
      can(regex("^[0-9A-HJKMNP-TV-Z]{26}$", k.id)) && can(regex("^[0-9a-f]{64}$", k.secret_sha256))
    ])
    error_message = "Each operator key needs a ULID id and a secret_sha256 of 64 lower-case hex digits."
  }
}

variable "subnet_cidr" {
  description = "The subnet Cloud Run's Direct VPC egress takes its addresses from. Each instance uses addresses from it, and Google recommends at least a /26."
  type        = string
  default     = "10.20.0.0/24"

  validation {
    condition     = can(cidrnetmask(var.subnet_cidr)) && tonumber(split("/", var.subnet_cidr)[1]) <= 26
    error_message = "subnet_cidr must be an IPv4 CIDR block of /26 or larger."
  }
}

variable "database_tier" {
  description = "The Cloud SQL machine type, of the Enterprise edition."
  type        = string
  default     = "db-custom-1-3840"
}
