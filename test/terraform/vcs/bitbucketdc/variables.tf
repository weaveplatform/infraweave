variable "host" {
  description = "The Bitbucket Data Center container's https URL."
  type        = string
  default     = "https://localhost:7990"
}

variable "ca_file" {
  description = "A PEM file of the certificate the container serves."
  type        = string
}

variable "admin_username" {
  description = "The container's system administrator."
  type        = string
  default     = "admin"
}

variable "admin_password" {
  description = "The system administrator's password."
  type        = string
  sensitive   = true
}

variable "project" {
  description = "The key of the project that holds the repositories."
  type        = string
  default     = "LAB"
}

variable "suffix" {
  description = "What makes the run's repository names unique: the Unix time the run starts, then its ID, so sweep.sh reads a leftover's age from its name."
  type        = string

  validation {
    condition     = can(regex("^[0-9]+-[0-9A-Za-z-]+$", var.suffix))
    error_message = "The suffix is the run's start as Unix time, a hyphen, then its ID."
  }
}
