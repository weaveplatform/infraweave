variable "subscription_id" {
  description = "The dedicated lab subscription the managed cloud worker batches use: landing-zone/azure's subscription_id output."
  type        = string
}

variable "location" {
  description = "The resource groups' region, and the region the worker stages create clusters in."
  type        = string
  default     = "uksouth"
}

variable "subject" {
  description = "The token subject that may sign in as the identity: the repository's immutable prefix (owner and repository IDs) and the workers-cloud environment, whose branch rule admits only main."
  type        = string
  default     = "repo:weaveplatform@336817058/infraweave@1436683462:environment:workers-cloud"
}

variable "assignable_roles" {
  description = "The built-in roles the identity may assign, inside the worker resource group and only to service principals: what an AKS cluster's control plane, kubelet and Workload ID identities and the identity's own cluster access need."
  type        = list(string)
  default = [
    "Network Contributor",
    "Managed Identity Operator",
    "AcrPull",
    "Azure Kubernetes Service RBAC Cluster Admin",
    "Azure Kubernetes Service RBAC Admin",
    "Azure Kubernetes Service Cluster User Role",
  ]

  validation {
    condition     = length(var.assignable_roles) > 0 && !anytrue([for r in var.assignable_roles : contains(["Owner", "User Access Administrator", "Role Based Access Control Administrator"], r)])
    error_message = "assignable_roles must name at least one role and no role that can itself assign roles."
  }
}
