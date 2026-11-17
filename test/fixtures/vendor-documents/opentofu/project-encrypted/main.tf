terraform {
  required_providers {
    random = { source = "hashicorp/random" }
  }
}

variable "api_key" {
  type      = string
  sensitive = true
}

resource "random_password" "db" {
  length  = 16
  special = false
}

resource "terraform_data" "config" {
  input = {
    api_key = var.api_key
    plain   = "visible"
  }
}

output "password" {
  value     = random_password.db.result
  sensitive = true
}

output "plain" {
  value = "visible"
}
