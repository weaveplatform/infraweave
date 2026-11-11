# The vcs-lab job's Bitbucket Cloud lab: two private repositories in
# the lab workspace, each holding seed.sh's history. Bitbucket Cloud mints
# a run no narrower token, so a run borrows the integration's, which
# reaches both.

locals {
  names = {
    lab   = "infraweave-lab-${var.suffix}"
    other = "infraweave-lab-${var.suffix}-other"
  }
}

resource "bitbucket_repository" "lab" {
  for_each = local.names

  owner       = var.workspace
  name        = each.value
  slug        = each.value
  project_key = var.project_key
  is_private  = true
  fork_policy = "no_forks"
}

# SEED_USER and SEED_TOKEN come from the environment that runs apply.
resource "terraform_data" "seed" {
  for_each = local.names

  triggers_replace = bitbucket_repository.lab[each.key].uuid

  provisioner "local-exec" {
    command = "${path.module}/../seed.sh"
    environment = {
      SEED_URL = "https://bitbucket.org/${var.workspace}/${bitbucket_repository.lab[each.key].slug}.git"
    }
  }
}
