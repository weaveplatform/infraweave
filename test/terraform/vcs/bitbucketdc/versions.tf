terraform {
  required_version = ">= 1.16.0"

  required_providers {
    restapi = {
      source  = "Mastercard/restapi"
      version = "3.0.0"
    }
  }
}

# The container's system administrator, whose password the job makes for
# the run, writes through Bitbucket's REST API; the container's
# certificate is the job's own, which root_ca_file trusts.
provider "restapi" {
  uri                  = var.host
  username             = var.admin_username
  password             = var.admin_password
  root_ca_file         = var.ca_file
  write_returns_object = true
  headers = {
    "X-Atlassian-Token" = "no-check"
  }
}
