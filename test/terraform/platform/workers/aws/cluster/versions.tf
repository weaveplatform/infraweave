terraform {
  required_version = ">= 1.16.0"

  # The lab coordinator supplies an owner-private state path.
  backend "local" {}

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "6.73.0"
    }
  }
}

# The caller's own credentials, which must already resolve to the lab
# account: the coordinator's preflight checks the CLI's identity against
# cloud.identifiers, and this provider refuses any other account before it
# reads or changes anything.
provider "aws" {
  region              = var.cloud.identifiers.region
  allowed_account_ids = [var.cloud.identifiers.account_id]

  # Every taggable resource carries the batch's ownership labels, which the
  # workers identity requires on a new cluster and cleanup's tag search
  # reads back, and the owner's expiry.
  default_tags {
    tags = merge(var.ownership.labels, { "io.infraweave.lab.expires-at" = var.ownership.expires_at })
  }
}
