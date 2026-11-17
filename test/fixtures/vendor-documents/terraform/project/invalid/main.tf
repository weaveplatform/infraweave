variable "session_token" {
  type      = string
  ephemeral = true
}

resource "terraform_data" "leaks" {
  input = var.session_token
}

output "missing" {
  value = terraform_data.absent.id
}
