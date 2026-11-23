variable "project_id" {
  description = "The lab project the KEK live tests use."
  type        = string
}

variable "location" {
  description = "The key ring's location."
  type        = string
  default     = "global"
}
