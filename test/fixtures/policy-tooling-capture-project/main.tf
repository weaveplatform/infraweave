# Dummy values let the live policy-input capture lab observe sensitive-field
# handling without credentials, providers, or infrastructure.
terraform {
  required_version = ">= 1.5.0, < 2.0.0"
}

variable "capture_secret" {
  type      = string
  default   = "capture-644-dummy-secret"
  sensitive = true
}

output "visible" {
  value = "capture-644-public"
}

output "secret" {
  value     = var.capture_secret
  sensitive = true
}
