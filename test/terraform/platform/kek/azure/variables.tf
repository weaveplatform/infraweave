variable "subscription_id" {
  description = "The lab subscription the KEK live tests use."
  type        = string
}

variable "location" {
  description = "The resource group's and vault's region."
  type        = string
  default     = "uksouth"
}
