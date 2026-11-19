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

resource "restapi_object" "hook" {
  count        = var.hook_url == "" ? 0 : 1
  path         = "/rest/api/1.0/projects/${restapi_object.project.id}/repos/${restapi_object.repository["lab"].id}/webhooks"
  id_attribute = "id"
  data = jsonencode({
    name   = "infraweave lab verification"
    url    = var.hook_url
    secret = var.hook_secret
    active = true
    events = [
      "repo:refs_changed", "pr:opened", "pr:from_ref_updated", "pr:modified",
      "pr:reviewer:approved", "pr:reviewer:unapproved", "pr:merged", "pr:declined", "pr:comment:added",
    ]
  })
  ignore_server_additions = true
}
