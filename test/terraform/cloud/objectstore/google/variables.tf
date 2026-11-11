variable "project_id" {
  description = "The lab project the objectstore-cloud job uses, and nothing else."
  type        = string
}

variable "location" {
  description = "The bucket's location."
  type        = string
  default     = "us-central1"
}

variable "repository" {
  description = "The GitHub repository whose workflow may federate, as owner/name."
  type        = string
  default     = "weaveplatform/infraweave"
}

variable "repository_id" {
  description = "The repository's numeric ID, which survives a rename or transfer that its name does not."
  type        = string
  default     = "1436683462"
}

variable "workflow" {
  description = "The one workflow file that may federate, on main."
  type        = string
  default     = ".github/workflows/objectstore-cloud.yml"
}
