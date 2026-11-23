variable "region" {
  description = "The provider's region. Organizations is global, so the account is not bound to it."
  type        = string
  default     = "eu-north-1"
}

variable "account_name" {
  description = "The member account's name, which is also the name of the organizational unit holding it."
  type        = string
  default     = "infraweave-lab"
}

variable "email" {
  description = "The member account's root user email. AWS requires it to be unique across all AWS accounts, and keeps it reserved after the account closes."
  type        = string

  validation {
    condition     = can(regex("^[^@\\s]+@[^@\\s]+\\.[^@\\s]+$", var.email))
    error_message = "email must be an email address."
  }
}

variable "parent_id" {
  description = "The organization root or organizational unit the lab's organizational unit sits under. Null places it under the organization's root."
  type        = string
  default     = null
}

variable "close_on_destroy" {
  description = "Whether destroying the root closes the member account. Organizations cannot remove an account it created that has no billing details of its own, so without this a destroy fails and the account stays. AWS suspends a closed account for 90 days before deleting it."
  type        = bool
  default     = false
}
