terraform {
  required_providers {
    random = { source = "hashicorp/random" }
  }
}

variable "environment" {
  type = string
}

variable "cidr" {
  type = string
}

variable "api_key" {
  type      = string
  sensitive = true
}

resource "terraform_data" "network" {
  input = {
    environment = var.environment
    cidr        = var.cidr
    api_key     = var.api_key
  }
}

resource "random_password" "db" {
  length  = 16
  special = false
}

output "network_id" {
  value = terraform_data.network.id
}

output "password" {
  value     = random_password.db.result
  sensitive = true
}
