variable "cidr" {
  type = string
}

resource "terraform_data" "network" {
  input = var.cidr
}

output "network_id" {
  value = terraform_data.network.id
}
