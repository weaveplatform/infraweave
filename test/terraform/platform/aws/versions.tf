terraform {
  required_version = ">= 1.16.0"

  # The lab controller supplies an owner-private state path.
  backend "local" {}

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "6.74.0"
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

  # Every taggable resource carries the batch's ownership labels, which
  # cleanup's owner tag search reads back after destroy.
  default_tags {
    tags = var.ownership.labels
  }
}
