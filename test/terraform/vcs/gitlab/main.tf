# The vcs-lab job's GitLab lab: two private projects in the lab group,
# each holding seed.sh's history, and the integration's token: a group
# access token at Maintainer, which reaches both projects and may mint a
# project access token, so a run's token minted for the first must not
# reach the second.

locals {
  names = {
    lab   = "infraweave-lab-${var.suffix}"
    other = "infraweave-lab-${var.suffix}-other"
  }
}

data "gitlab_group" "lab" {
  full_path = var.group
}

resource "gitlab_project" "lab" {
  for_each = local.names

  name                   = each.value
  path                   = each.value
  namespace_id           = data.gitlab_group.lab.id
  visibility_level       = "private"
  initialize_with_readme = false
  # A group that delays deletion would otherwise keep the project.
  permanently_delete_on_destroy = true
}

resource "time_offset" "token" {
  offset_days = 2
}

resource "gitlab_group_access_token" "integration" {
  group        = data.gitlab_group.lab.id
  name         = "infraweave-lab-${var.suffix}"
  access_level = "maintainer"
  scopes       = ["api"]
  expires_at   = formatdate("YYYY-MM-DD", time_offset.token.rfc3339)
}

# SEED_USER and SEED_TOKEN come from the environment that runs apply.
resource "terraform_data" "seed" {
  for_each = local.names

  triggers_replace = gitlab_project.lab[each.key].id

  provisioner "local-exec" {
    command = "${path.module}/../seed.sh"
    environment = {
      SEED_URL = gitlab_project.lab[each.key].http_url_to_repo
    }
  }
}
