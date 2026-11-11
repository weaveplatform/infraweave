terraform {
  required_version = ">= 1.16.0"

  required_providers {
    bitbucket = {
      source  = "DrFaust92/bitbucket"
      version = "2.55.0"
    }
  }
}

# BITBUCKET_USERNAME and BITBUCKET_PASSWORD hold the lab account's e-mail
# and an API token that creates and deletes the workspace's repositories.
provider "bitbucket" {}
