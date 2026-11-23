variable "project_id" {
  description = "The lab project's ID, which is unique across Google Cloud and cannot be reused for 30 days after the project is deleted."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.project_id))
    error_message = "project_id must be 6 to 30 lowercase letters, digits or hyphens, starting with a letter and not ending with a hyphen."
  }
}

variable "project_name" {
  description = "The lab project's display name."
  type        = string
  default     = "infraweave-lab"
}

variable "billing_account" {
  description = "The billing account the project is linked to, as its ID (XXXXXX-XXXXXX-XXXXXX)."
  type        = string
}

variable "org_id" {
  description = "The organization the project is created in. Exactly one of org_id and folder_id is set."
  type        = string
  default     = null
}

variable "folder_id" {
  description = "The folder the project is created in, as its numeric ID. Exactly one of org_id and folder_id is set."
  type        = string
  default     = null

  validation {
    condition     = (var.org_id == null) != (var.folder_id == null)
    error_message = "Set exactly one of org_id and folder_id."
  }
}

variable "services" {
  description = "The APIs the platform roots use in the project: Workload Identity Federation and IAM for every CI identity, Cloud Storage for the object store, Cloud KMS for the KEK, and Compute Engine, GKE and Cloud Asset Inventory for the worker clusters."
  type        = list(string)
  default = [
    "cloudasset.googleapis.com",
    "cloudkms.googleapis.com",
    "cloudresourcemanager.googleapis.com",
    "compute.googleapis.com",
    "container.googleapis.com",
    "iam.googleapis.com",
    "iamcredentials.googleapis.com",
    "serviceusage.googleapis.com",
    "storage.googleapis.com",
    "sts.googleapis.com",
  ]
}

variable "delete_on_destroy" {
  description = "Whether destroying the root deletes the project. Without this a destroy fails and the project stays. Google keeps a deleted project recoverable for 30 days."
  type        = bool
  default     = false
}
