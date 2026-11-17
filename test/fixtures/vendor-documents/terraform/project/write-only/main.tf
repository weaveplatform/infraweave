terraform {
  required_providers {
    random = { source = "hashicorp/random" }
  }
}

ephemeral "random_password" "session" {
  length  = 20
  special = false
}

resource "terraform_data" "stored_secret" {
  store {
    input     = ephemeral.random_password.session.result
    version   = 1
    sensitive = true
  }
}

resource "terraform_data" "stored_plain" {
  store {
    input   = "dummy-write-only-plain"
    version = 1
  }
}
