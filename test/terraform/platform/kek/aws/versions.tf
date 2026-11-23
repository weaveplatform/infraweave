terraform {
  required_version = ">= 1.16.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "6.73.0"
    }
  }
}

provider "aws" {
  region = var.region
  # Credentials that resolve to any account other than the landing zone's
  # fail before Terraform reads or changes anything.
  allowed_account_ids = var.landing_zone_role_arn == null ? null : [split(":", var.landing_zone_role_arn)[4]]

  dynamic "assume_role" {
    for_each = var.landing_zone_role_arn == null ? [] : [var.landing_zone_role_arn]
    content {
      role_arn = assume_role.value
    }
  }
}
