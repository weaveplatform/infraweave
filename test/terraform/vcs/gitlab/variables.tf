variable "host" {
  description = "The GitLab instance."
  type        = string
  default     = "https://gitlab.com"
}

variable "group" {
  description = "The lab group's full path, which holds the projects."
  type        = string
}

variable "suffix" {
  description = "What makes the run's project names unique: the Unix time the run starts, then its ID, so sweep.sh reads a leftover's age from its name."
  type        = string

  validation {
    condition     = can(regex("^[0-9]+-[0-9A-Za-z-]+$", var.suffix))
    error_message = "The suffix is the run's start as Unix time, a hyphen, then its ID."
  }
}
