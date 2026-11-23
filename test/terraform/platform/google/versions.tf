terraform {
  required_version = ">= 1.16.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "8.12.0"
    }
  }

  # cloudlab.py supplies an owner-private state path at init.
  backend "local" {}
}

provider "google" {
  project = var.project_id
  region  = var.region
}
