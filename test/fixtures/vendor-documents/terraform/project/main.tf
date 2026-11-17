resource "random_pet" "name" {
  length = 2
}

resource "random_password" "db" {
  length  = 16
  special = false
}

resource "terraform_data" "config" {
  input = {
    name    = random_pet.name.id
    api_key = var.api_key
    plain   = "visible-v1"
    marked  = sensitive("dummy-marked-v1")
  }
}

resource "terraform_data" "old" {
  input = "moves"
}

resource "terraform_data" "forget" {
  input = "kept-outside"
}

resource "terraform_data" "dropped" {
  input = "deleted"
}

resource "local_file" "rendered" {
  filename = "${path.module}/out/rendered.txt"
  content  = "name=${random_pet.name.id}\n"
}

resource "local_sensitive_file" "secret" {
  filename = "${path.module}/out/secret.txt"
  content  = "api_key=${var.api_key}\n"
}

ephemeral "random_password" "session" {
  length  = 20
  special = false
}

module "session" {
  source = "./modules/session"
  token  = var.session_token
}

locals {
  session_header = module.session.header
}

resource "terraform_data" "uses_ephemeral" {
  input = "runs-a-provisioner"

  provisioner "local-exec" {
    command = "test -n \"$SESSION\" && test -n \"$HEADER\""
    environment = {
      SESSION = ephemeral.random_password.session.result
      HEADER  = local.session_header
    }
  }
}

check "api_key_rotated" {
  assert {
    condition     = var.api_key != "dummy-api-key-v1"
    error_message = "The API key is still the first one."
  }
}

output "name" {
  value = random_pet.name.id
}

output "password" {
  value     = random_password.db.result
  sensitive = true
}

output "api_key" {
  value     = var.api_key
  sensitive = true
}

output "file_id" {
  value = local_file.rendered.id
}

output "nested" {
  value = {
    plain  = "visible"
    marked = sensitive("dummy-nested-v1")
  }
  sensitive = true
}
