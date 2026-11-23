variable "project_id" {
  description = "The dedicated lab project the KEK live tests use: landing-zone/google's project_id output."
  type        = string
}

variable "location" {
  description = "The key ring's location."
  type        = string
  default     = "global"
}
