variable "organization" {
  description = "The lab organization, which holds the repositories."
  type        = string
}

variable "token" {
  description = "An organization owner's token, which creates and deletes repositories; TF_VAR_token sets it."
  type        = string
  sensitive   = true
}

variable "suffix" {
  description = "What makes the run's repository names unique: the Unix time the run starts, then its ID, so sweep.sh reads a leftover's age from its name."
  type        = string

  validation {
    condition     = can(regex("^[0-9]+-[0-9A-Za-z-]+$", var.suffix))
    error_message = "The suffix is the run's start as Unix time, a hyphen, then its ID."
  }
}
