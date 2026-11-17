variable "token" {
  type      = string
  ephemeral = true
}

output "header" {
  value     = "Bearer ${var.token}"
  ephemeral = true
}
