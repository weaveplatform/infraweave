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

resource "gitlab_project_hook" "lab" {
  count                 = var.hook_url == "" ? 0 : 1
  project               = gitlab_project.lab["lab"].id
  url                   = var.hook_url
  token                 = var.hook_secret
  enable_ssl_verification = true
  push_events           = true
  tag_push_events       = true
  merge_requests_events = true
  note_events           = true
}
