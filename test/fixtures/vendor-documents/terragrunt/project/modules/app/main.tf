variable "environment" {
  type = string
}

variable "network_id" {
  type = string
}

variable "password" {
  type      = string
  sensitive = true
}

resource "terraform_data" "app" {
  input = {
    environment = var.environment
    network_id  = var.network_id
    password    = var.password
  }
}

output "app_id" {
  value = terraform_data.app.id
}
