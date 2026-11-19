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

resource "bitbucket_hook" "lab" {
  count                  = var.hook_url == "" ? 0 : 1
  owner                  = var.workspace
  repository             = bitbucket_repository.lab["lab"].slug
  url                    = var.hook_url
  secret                 = var.hook_secret
  description            = "infraweave lab verification"
  active                 = true
  skip_cert_verification = false
  events = [
    "repo:push", "pullrequest:created", "pullrequest:updated",
    "pullrequest:approved", "pullrequest:unapproved", "pullrequest:fulfilled",
    "pullrequest:rejected", "pullrequest:comment_created",
  ]
}
