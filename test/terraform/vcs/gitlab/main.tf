# The vcs-lab job's GitLab lab, on the container the job starts: a group
# with two private projects, each holding seed.sh's history, and the
# integration's token: the personal access token of a user who is the
# group's Maintainer, which reaches both projects and may mint a project
# access token, so a run's token minted for the first must not reach the
# second. A group access token would reach both too, but GitLab refuses
# its bot user a project access token.

locals {
  names = {
    lab   = "infraweave-lab-${var.suffix}"
    other = "infraweave-lab-${var.suffix}-other"
  }
}

resource "gitlab_group" "lab" {
  name             = "infraweave lab"
  path             = var.group
  visibility_level = "private"
  # Destroying the group only marks it for deletion, as GitLab refuses to
  # remove it at once; the container goes when the run ends.
}

resource "gitlab_project" "lab" {
  for_each = local.names

  name                   = each.value
  path                   = each.value
  namespace_id           = gitlab_group.lab.id
  visibility_level       = "private"
  initialize_with_readme = false
  # A group that delays deletion would otherwise keep the project.
  permanently_delete_on_destroy = true
}

resource "time_offset" "token" {
  offset_days = 2
}

resource "gitlab_user" "integration" {
  name                  = "infraweave lab"
  username              = "infraweave-lab"
  email                 = "lab@infraweave.invalid"
  force_random_password = true
  skip_confirmation     = true
}

resource "gitlab_group_membership" "integration" {
  group_id     = gitlab_group.lab.id
  user_id      = gitlab_user.integration.id
  access_level = "maintainer"
}

resource "gitlab_personal_access_token" "integration" {
  user_id    = gitlab_user.integration.id
  name       = "infraweave-lab-${var.suffix}"
  scopes     = ["api"]
  expires_at = formatdate("YYYY-MM-DD", time_offset.token.rfc3339)
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
