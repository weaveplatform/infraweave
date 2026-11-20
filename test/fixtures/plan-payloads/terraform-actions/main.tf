terraform {
  required_providers {
    local = {
      source = "hashicorp/local"
      version = "2.9.1"
    }
  }
}

variable "secret" {
  type = string
  sensitive = true
}

resource "terraform_data" "seed" {
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
    arguments = ["visible-675", var.secret, terraform_data.seed.id]
    stdin = var.secret
    environment = {
      public = "visible-675"
      private = var.secret
      copy = "dummy-action-secret-675"
    }
  }
}
