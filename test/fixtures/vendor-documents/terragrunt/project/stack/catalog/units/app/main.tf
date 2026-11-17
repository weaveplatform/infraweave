variable "network_id" {
  type = string
}

resource "terraform_data" "app" {
  input = var.network_id
}
