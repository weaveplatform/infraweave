# The vcs-lab job's Azure DevOps lab: two repositories in the lab
# project, each holding seed.sh's history. Azure DevOps mints a run no
# narrower token, so a run borrows the integration's, which reaches both.

locals {
  names = {
    lab   = "infraweave-lab-${var.suffix}"
    other = "infraweave-lab-${var.suffix}-other"
  }
}

data "azuredevops_project" "lab" {
  name = var.project
}

resource "azuredevops_git_repository" "lab" {
  for_each = local.names

  project_id = data.azuredevops_project.lab.id
  name       = each.value

  # seed.sh pushes to an empty repository.
  initialization {
    init_type = "Uninitialized"
  }
}

# SEED_USER and SEED_TOKEN come from the environment that runs apply.
resource "terraform_data" "seed" {
  for_each = local.names

  triggers_replace = azuredevops_git_repository.lab[each.key].id

  provisioner "local-exec" {
    command = "${path.module}/../seed.sh"
    environment = {
      SEED_URL = azuredevops_git_repository.lab[each.key].remote_url
    }
  }
}
