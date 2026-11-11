variable "host" {
  description = "The GitLab container's https URL."
  type        = string
  default     = "https://localhost:8443"
}

variable "ca_file" {
  description = "A PEM file of the certificate the container serves."
  type        = string
}

variable "group" {
  description = "The path of the group that holds the projects."
  type        = string
  default     = "lab"
}

variable "suffix" {
  description = "What makes the run's project names unique: the Unix time the run starts, then its ID, so sweep.sh reads a leftover's age from its name."
  type        = string

  validation {
    condition     = can(regex("^[0-9]+-[0-9A-Za-z-]+$", var.suffix))
    error_message = "The suffix is the run's start as Unix time, a hyphen, then its ID."
  }
}
