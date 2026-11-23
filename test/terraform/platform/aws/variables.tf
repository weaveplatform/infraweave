variable "region" {
  description = "The region every resource of the platform lives in."
  type        = string
  default     = "eu-north-1"
}

variable "landing_zone_role_arn" {
  description = "The role the provider assumes in the dedicated lab account: landing-zone/aws's role_arn output. Null applies into the account of the caller's own credentials."
  type        = string
  default     = null

  validation {
    condition     = var.landing_zone_role_arn == null || can(regex("^arn:aws[a-z-]*:iam::[0-9]{12}:role/.+$", var.landing_zone_role_arn))
    error_message = "landing_zone_role_arn must be an IAM role ARN."
  }
}

variable "ownership" {
  description = "The batch that owns this platform, as cloudlab.py supplies it to every stage: owner, batch, an RFC 3339 expires_at, and the io.infraweave.lab.* labels every resource is tagged with."
  type = object({
    owner      = string
    batch      = string
    expires_at = string
    labels     = map(string)
  })

  validation {
    condition     = can(regex("^[A-Za-z0-9._-]{1,63}$", var.ownership.owner)) && var.ownership.batch != ""
    error_message = "ownership.owner must be 1 to 63 letters, digits, dots, underscores or hyphens, and ownership.batch must be set."
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
  description = "The infraweave server image, pinned by digest (<repository>@sha256:<digest>). Its entrypoint is infraweave all; the tasks pass it only --config."
  type        = string

  validation {
    condition     = can(regex("^[^@\\s]+@sha256:[0-9a-f]{64}$", var.image))
    error_message = "image must be pinned by digest: <repository>@sha256:<64 hex digits>."
  }
}

# The default is the PostgreSQL build the acceptance tests run.
# ref: test/deps/images.go ImagePostgres
variable "bootstrap_image" {
  description = "The image of the tasks' bootstrap container, which writes the server's configuration file and creates the server's IAM-authenticated database role with psql. It must hold sh and psql, and is pinned by digest."
  type        = string
  default     = "docker.io/library/postgres:18.7@sha256:eb63372c1e05c755f55fa30cbd07aecc443676fc6c0fa90b9002d01537841e1a"

  validation {
    condition     = can(regex("^[^@\\s]+@sha256:[0-9a-f]{64}$", var.bootstrap_image))
    error_message = "bootstrap_image must be pinned by digest: <repository>@sha256:<64 hex digits>."
  }
}

variable "domain_name" {
  description = "The lab domain the platform answers at, server.public_url's host. It lies in route53_zone_id's zone, where its ACM certificate is validated and its alias record points at the load balancer."
  type        = string

  validation {
    condition     = can(regex("^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\\.)+[a-z]{2,63}$", var.domain_name))
    error_message = "domain_name must be a lower-case DNS name."
  }
}

variable "route53_zone_id" {
  description = "The public Route 53 hosted zone, in the same account, that holds domain_name. The platform borrows it: its validation and alias records are created there, and the zone itself is never changed."
  type        = string

  validation {
    condition     = can(regex("^Z[A-Z0-9]{1,31}$", var.route53_zone_id))
    error_message = "route53_zone_id must be a Route 53 hosted zone ID."
  }
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

variable "ingress_cidrs" {
  description = "The IPv4 ranges that may reach the load balancer on 443: the acceptance runner and the worker clusters of all three clouds, so the default admits any address."
  type        = list(string)
  default     = ["0.0.0.0/0"]

  validation {
    condition     = length(var.ingress_cidrs) > 0 && alltrue([for c in var.ingress_cidrs : can(cidrnetmask(c))])
    error_message = "ingress_cidrs must hold at least one IPv4 CIDR block."
  }
}

variable "vpc_cidr" {
  description = "The VPC's IPv4 range, split into a public and a private subnet in each of two availability zones."
  type        = string
  default     = "10.64.0.0/16"

  validation {
    condition     = can(cidrnetmask(var.vpc_cidr)) && tonumber(split("/", var.vpc_cidr)[1]) <= 20
    error_message = "vpc_cidr must be an IPv4 CIDR block of /20 or larger."
  }
}

variable "cpu_architecture" {
  description = "The tasks' CPU architecture. Both images must hold a build for it."
  type        = string
  default     = "ARM64"

  validation {
    condition     = contains(["ARM64", "X86_64"], var.cpu_architecture)
    error_message = "cpu_architecture must be ARM64 or X86_64."
  }
}

variable "task_cpu" {
  description = "Each task's CPU units, of a Fargate size task_memory fits."
  type        = number
  default     = 1024
}

variable "task_memory" {
  description = "Each task's memory in MiB."
  type        = number
  default     = 2048
}

variable "postgres_version" {
  description = "The RDS for PostgreSQL engine version. infraweave refuses a server below PostgreSQL 18."
  type        = string
  default     = "18"

  validation {
    condition     = can(regex("^(1[89]|[2-9][0-9])(\\.[0-9]+)?$", var.postgres_version))
    error_message = "postgres_version must be 18 or newer."
  }
}

variable "db_instance_class" {
  description = "The RDS instance class. Two tasks of 20 connections each, with their listeners, fit the smallest class's connection limit."
  type        = string
  default     = "db.t4g.micro"
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

variable "log_retention_days" {
  description = "How many days CloudWatch keeps the tasks' logs."
  type        = number
  default     = 7
}
