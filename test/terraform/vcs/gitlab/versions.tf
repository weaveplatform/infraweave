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

# GITLAB_TOKEN holds the container's administrator's token, which the
# job makes for the run; the container's certificate is the job's own,
# which cacert_file trusts.
provider "gitlab" {
  base_url    = "${var.host}/api/v4/"
  cacert_file = var.ca_file
}
