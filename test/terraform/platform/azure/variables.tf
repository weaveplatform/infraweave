variable "subscription_id" {
  description = "The dedicated lab subscription the platform lives in: landing-zone/azure's subscription_id output."
  type        = string

  validation {
    condition     = can(regex("^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$", var.subscription_id))
    error_message = "subscription_id must be a subscription ID, a lower-case GUID."
  }
}

variable "location" {
  description = "The region every resource of the platform lives in."
  type        = string
  default     = "uksouth"
}

variable "ownership" {
  description = "The batch that owns this platform, as cloudlab.py supplies it to every stage: owner, batch, an RFC 3339 expires_at, and the io.infraweave.lab.* labels every resource is tagged with."
  type = object({
    owner      = string
    batch      = string
    expires_at = string
    labels     = map(string)
  })

  # The owner names the resource group, and resource group names admit
  # these characters; the owner search compares the tag value exactly.
  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9-]{0,62}$", var.ownership.owner)) && var.ownership.batch != ""
    error_message = "ownership.owner must be 1 to 63 lower-case letters, digits or hyphens, starting with a letter or digit, and ownership.batch must be set."
  }

  validation {
    condition     = can(timeadd(var.ownership.expires_at, "0s"))
    error_message = "ownership.expires_at must be an RFC 3339 timestamp."
  }

  # Cleanup finds what is left by this tag, so a label naming another
  # owner would hide this platform's resources from it.
  validation {
    condition     = lookup(var.ownership.labels, "io.infraweave.lab.owner", null) == var.ownership.owner
    error_message = "ownership.labels must carry io.infraweave.lab.owner with ownership.owner's value."
  }
}

variable "image" {
  description = "The infraweave server image, pinned by digest (<repository>@sha256:<digest>), which Container Apps pulls anonymously. Its entrypoint is infraweave all; the app passes it only --config."
  type        = string

  validation {
    condition     = can(regex("^[^@\\s]+@sha256:[0-9a-f]{64}$", var.image))
    error_message = "image must be pinned by digest: <repository>@sha256:<64 hex digits>."
  }
}

variable "domain_name" {
  description = "The lab domain the platform answers at, server.public_url's host. It lies below dns_zone, where its verification and CNAME records are created, and Container Apps issues its managed certificate."
  type        = string

  validation {
    condition     = can(regex("^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\\.)+[a-z]{2,63}$", var.domain_name))
    error_message = "domain_name must be a lower-case DNS name."
  }

  # A CNAME cannot sit at a zone's apex.
  validation {
    condition     = endswith(var.domain_name, ".${var.dns_zone.name}")
    error_message = "domain_name must be a name below dns_zone.name, not its apex."
  }
}

variable "dns_zone" {
  description = "The public Azure DNS zone, in the same subscription, that holds domain_name. The platform borrows it: its records are created there, and the zone itself is never changed."
  type = object({
    name                = string
    resource_group_name = string
  })
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

variable "vnet_cidr" {
  description = "The virtual network's IPv4 range: a /23 for the Container Apps environment and a /24 for PostgreSQL are taken from its start."
  type        = string
  default     = "10.72.0.0/16"

  validation {
    condition     = can(cidrnetmask(var.vnet_cidr)) && tonumber(split("/", var.vnet_cidr)[1]) <= 22
    error_message = "vnet_cidr must be an IPv4 CIDR block of /22 or larger."
  }
}

variable "cpu" {
  description = "Each replica's vCPUs, of a Consumption profile size that memory fits."
  type        = number
  default     = 1
}

variable "memory" {
  description = "Each replica's memory, twice its vCPUs in Gi on the Consumption profile."
  type        = string
  default     = "2Gi"
}

variable "postgres_sku" {
  description = "The PostgreSQL Flexible Server SKU. Burstable B1ms allows 50 connections, of which Azure reserves 15, fewer than two replicas' pools of 20 and their listening connections need; B2s allows several hundred."
  type        = string
  default     = "B_Standard_B2s"
}

variable "log_level" {
  description = "log.level: debug, info, warn or error."
  type        = string
  default     = "info"

  validation {
    condition     = contains(["debug", "info", "warn", "error"], var.log_level)
    error_message = "log_level must be debug, info, warn or error."
  }
}
