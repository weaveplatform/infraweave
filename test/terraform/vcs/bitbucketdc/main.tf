# The vcs-lab job's Bitbucket Data Center lab, on the container the job
# starts: a project with two repositories, each holding seed.sh's
# history, and the integration's token: a project access token with
# project admin, which reaches both repositories and may mint a
# repository access token, so a run's token minted for the first must
# not reach the second.

locals {
  names = {
    lab   = "infraweave-lab-${var.suffix}"
    other = "infraweave-lab-${var.suffix}-other"
  }
}

resource "restapi_object" "project" {
  path         = "/rest/api/1.0/projects"
  id_attribute = "key"
  data = jsonencode({
    key  = var.project
    name = "infraweave lab"
  })
}

resource "restapi_object" "repository" {
  for_each = local.names

  path         = "/rest/api/1.0/projects/${restapi_object.project.id}/repos"
  id_attribute = "slug"
  data = jsonencode({
    name  = each.value
    scmId = "git"
  })
  ignore_server_additions = true
}

# Bitbucket answers a token's value once, on creation.
resource "restapi_object" "token" {
  path          = "/rest/access-tokens/1.0/projects/${restapi_object.project.id}"
  create_method = "PUT"
  id_attribute  = "id"
  data = jsonencode({
    name        = "infraweave-lab-${var.suffix}"
    permissions = ["PROJECT_ADMIN"]
    expiryDays  = 1
  })
  ignore_server_additions = true
}

# SEED_USER and SEED_TOKEN come from the environment that runs apply.
resource "terraform_data" "seed" {
  for_each = local.names

  triggers_replace = restapi_object.repository[each.key].id

  provisioner "local-exec" {
    command = "${path.module}/../seed.sh"
    environment = {
      SEED_URL = "${var.host}/scm/${lower(var.project)}/${restapi_object.repository[each.key].id}.git"
    }
  }
}
