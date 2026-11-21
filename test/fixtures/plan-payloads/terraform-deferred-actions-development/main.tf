terraform {
  required_providers {
    local = { source = "hashicorp/local", version = "2.9.1" }
  }
}
variable "secret" {
  type = string
  sensitive = true
}
resource "terraform_data" "seed" { input = 1 }
resource "terraform_data" "deferred" {
  count = terraform_data.seed.output
  input = { private = var.secret, copy = "dummy-action-secret-675", public = "visible-675" }
}
resource "terraform_data" "trigger" {
  input = "visible-675"
  lifecycle {
    action_trigger {
      events = [after_create]
      actions = [action.local_command.capture]
    }
  }
}
action "local_command" "capture" {
  config {
    command = "true"
    stdin = var.secret
    arguments = ["visible-675", var.secret, terraform_data.deferred[0].id]
    environment = { public = "visible-675", private = var.secret, copy = "dummy-action-secret-675" }
  }
}
