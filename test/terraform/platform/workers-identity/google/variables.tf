variable "project_id" {
  description = "The dedicated lab project the managed cloud worker batches use: landing-zone/google's project_id output. The service account's GKE and network roles reach every cluster and network in it."
  type        = string
}

variable "repository" {
  description = "The GitHub repository whose workflow may federate, as owner/name."
  type        = string
  default     = "weaveplatform/infraweave"
}

variable "repository_id" {
  description = "The repository's numeric ID, which survives a rename or transfer that its name does not."
  type        = string
  default     = "1436683462"
}

variable "workflow" {
  description = "The one workflow file that may federate, on main."
  type        = string
  default     = ".github/workflows/managed-cloud-workers.yml"
}

variable "environment" {
  description = "The GitHub environment the workflow's job runs in."
  type        = string
  default     = "workers-cloud"
}

variable "subject" {
  description = "The token subject that may impersonate the service account: the repository's immutable prefix (owner and repository IDs) and the workers-cloud environment, whose branch rule admits only main."
  type        = string
  default     = "repo:weaveplatform@336817058/infraweave@1436683462:environment:workers-cloud"
}

variable "grantable_roles" {
  description = "Project roles the service account may grant and revoke, for a stage that gives a Kubernetes service account Google Cloud access through Workload Identity Federation for GKE. Empty grants it no project IAM rights at all."
  type        = list(string)
  default     = []

  validation {
    condition     = length(var.grantable_roles) <= 10 && alltrue([for r in var.grantable_roles : can(regex("^roles/[A-Za-z0-9_.]+$", r))])
    error_message = "grantable_roles takes at most ten predefined roles, as roles/<name>."
  }

  validation {
    condition     = !anytrue([for r in var.grantable_roles : can(regex("(?i)(owner|editor|admin|setiampolicy|tokencreator|serviceaccountuser|workloadidentityuser)", r))])
    error_message = "grantable_roles must not name a role that grants access, administers a service or impersonates a service account."
  }
}
