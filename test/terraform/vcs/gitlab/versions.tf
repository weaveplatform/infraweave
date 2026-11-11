terraform {
  required_version = ">= 1.16.0"

  required_providers {
    gitlab = {
      source  = "gitlabhq/gitlab"
      version = "19.5.0"
    }
    time = {
      source  = "hashicorp/time"
      version = "0.14.2"
    }
  }
}

# GITLAB_TOKEN holds a token of an owner of the lab group, which creates
# and deletes projects and the group's access token.
provider "gitlab" {
  base_url = "${var.host}/api/v4/"
}
