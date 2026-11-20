variable "secret" {
  type = string
  sensitive = true
}
resource "terraform_data" "seed" {
  input = { private = var.secret, public = "visible-675" }
}
