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
    arguments = ["visible-675", sensitive(var.secret), terraform_data.seed.id]
    stdin = sensitive(var.secret)
    environment = {
      public = "visible-675"
      private = sensitive(var.secret)
      copy = var.secret
    }
  }
}
