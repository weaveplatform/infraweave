# The source contract seeds repositories first. The live webhook scenario then
# supplies the served integration URL and secret through a private var file;
# returning URL to empty detaches hooks before the integration is deleted.
variable "hook_url" {
  type        = string
  description = "The current scenario integration webhook URL; empty disables hooks."
  default     = ""
}

variable "hook_secret" {
  type        = string
  description = "The current integration signing secret or Azure password."
  sensitive   = true
  default     = ""
}

locals {
  # A separate native subscription is required for each event type.
  hook_events = var.hook_url == "" ? toset([]) : toset([
    "push", "created", "updated", "merged", "commented",
  ])
}

resource "azuredevops_servicehook_webhook_tfs" "lab" {
  for_each                 = local.hook_events
  project_id               = data.azuredevops_project.lab.id
  url                      = var.hook_url
  basic_auth_username      = ""
  basic_auth_password      = var.hook_secret
  accept_untrusted_certs   = false
  resource_details_to_send = "all"
  # The documented comment payload uses nested pullRequest/repository at version 2.0.
  # ref: https://learn.microsoft.com/en-us/azure/devops/service-hooks/events#pull-request-commented-on
  resource_version = each.key == "commented" ? "2.0" : "1.0"

  dynamic "git_push" {
    for_each = each.key == "push" ? [1] : []
    content { repository_id = azuredevops_git_repository.lab["lab"].id }
  }
  dynamic "git_pull_request_created" {
    for_each = each.key == "created" ? [1] : []
    content { repository_id = azuredevops_git_repository.lab["lab"].id }
  }
  dynamic "git_pull_request_updated" {
    for_each = each.key == "updated" ? [1] : []
    content { repository_id = azuredevops_git_repository.lab["lab"].id }
  }
  dynamic "git_pull_request_merge_attempted" {
    for_each = each.key == "merged" ? [1] : []
    content { repository_id = azuredevops_git_repository.lab["lab"].id }
  }
  dynamic "git_pull_request_commented" {
    for_each = each.key == "commented" ? [1] : []
    content { repository_id = azuredevops_git_repository.lab["lab"].id }
  }
}
