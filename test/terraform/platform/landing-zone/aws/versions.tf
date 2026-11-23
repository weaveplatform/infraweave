terraform {
  required_version = ">= 1.16.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "6.73.0"
    }
  }
}

# The Organization's management account, whose credentials alone may create
# a member account.
provider "aws" {
  region = var.region
}
