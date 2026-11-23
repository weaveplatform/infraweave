variable "region" {
  description = "The bucket's region."
  type        = string
  default     = "eu-north-1"
}

variable "github_oidc_provider_arn" {
  description = "An existing IAM OIDC provider for token.actions.githubusercontent.com; an account holds at most one, so one already there is reused rather than created."
  type        = string
  default     = null
}

variable "subject" {
  description = "The token subject that may assume the roles: the repository's immutable prefix (owner and repository IDs) and the objectstore-cloud environment, whose branch rule admits only main."
  type        = string
  default     = "repo:weaveplatform@336817058/infraweave@1436683462:environment:objectstore-cloud"
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
