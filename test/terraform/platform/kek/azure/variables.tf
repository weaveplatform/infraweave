variable "subscription_id" {
  description = "The dedicated lab subscription the KEK live tests use: landing-zone/azure's subscription_id output."
  type        = string
}

variable "location" {
  description = "The resource group's and vault's region."
  type        = string
  default     = "uksouth"
}
