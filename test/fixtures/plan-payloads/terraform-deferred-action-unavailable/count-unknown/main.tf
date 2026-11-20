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
resource "terraform_data" "trigger" {
  input = "visible-675"
  lifecycle {
    action_trigger {
      events = [after_create]
      actions = [action.local_command.capture[0]]
    }
  }
}
action "local_command" "capture" {
  count = terraform_data.seed.output
  config {
    command = "true"
    stdin = var.secret
    environment = { public = "visible-675", private = var.secret, copy = "dummy-action-secret-675" }
  }
}
