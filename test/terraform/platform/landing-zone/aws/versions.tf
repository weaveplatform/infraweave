terraform {
  required_version = ">= 1.16.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "6.74.0"
    }
  }
}

# The account that becomes the Organization's management account; only its
# credentials may create the Organization and a member account.
provider "aws" {
  region = var.region
}
