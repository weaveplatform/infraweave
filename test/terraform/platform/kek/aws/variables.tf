variable "region" {
  description = "The key's region."
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
