# The vcs-lab job's GitHub lab: two private repositories, each holding
# seed.sh's history. The lab App is installed on all of the
# organization's repositories, so it reaches the second and a run's token
# minted for the first must not.

locals {
  names = {
    lab   = "infraweave-lab-${var.suffix}"
    other = "infraweave-lab-${var.suffix}-other"
  }
}

resource "github_repository" "lab" {
  for_each = local.names

  name        = each.value
  description = "infraweave vcs-lab lab; the job deletes it"
  visibility  = "private"
  # seed.sh pushes to an empty repository.
  auto_init = false
}

# SEED_USER and SEED_TOKEN come from the environment that runs apply.
resource "terraform_data" "seed" {
  for_each = local.names

  triggers_replace = github_repository.lab[each.key].node_id

  provisioner "local-exec" {
    command = "${path.module}/../seed.sh"
    environment = {
      SEED_URL = github_repository.lab[each.key].http_clone_url
    }
  }
}
