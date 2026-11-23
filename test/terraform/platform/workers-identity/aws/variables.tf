variable "region" {
  description = "The one region the managed cloud worker batches use; every regional grant is limited to it."
  type        = string
  default     = "eu-north-1"
}

variable "github_oidc_provider_arn" {
  description = "An existing IAM OIDC provider for token.actions.githubusercontent.com; an account holds at most one, so one already there is reused rather than created."
  type        = string
  default     = null
}

variable "subject" {
  description = "The token subject that may assume the role: the repository's immutable prefix (owner and repository IDs) and the workers-cloud environment, whose branch rule admits only main."
  type        = string
  default     = "repo:weaveplatform@336817058/infraweave@1436683462:environment:workers-cloud"
}

variable "name_prefix" {
  description = "The prefix of every EKS cluster, IAM role, IAM policy and EKS log group the worker stages create; the role may change nothing else of those kinds."
  type        = string
  default     = "iw-workers-"

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{2,19}$", var.name_prefix))
    error_message = "name_prefix must be 3 to 20 lowercase letters, digits or hyphens, starting with a letter."
  }
}

variable "iam_path" {
  description = "The IAM path of every role and policy the worker stages create. EKS access entries accept a role ARN with a path; aws-auth ConfigMap entries do not, so the stages map roles through access entries."
  type        = string
  default     = "/infraweave-workers/"

  validation {
    condition     = can(regex("^/([A-Za-z0-9_+=,.@-]+/)+$", var.iam_path))
    error_message = "iam_path must start and end with a slash and name at least one segment."
  }
}

variable "existing_cluster_names" {
  description = "EKS clusters outside name_prefix that existing-cluster batches deploy onto; the role may manage Pod Identity associations on them and nothing else."
  type        = list(string)
  default     = []
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
