variable "subscription_id" {
  description = "The dedicated lab subscription the objectstore-cloud job uses: landing-zone/azure's subscription_id output."
  type        = string
}

variable "location" {
  description = "The resource group's and storage account's region."
  type        = string
  default     = "uksouth"
}

variable "subject" {
  description = "The token subject that may sign in as the identities: the repository's immutable prefix (owner and repository IDs) and the objectstore-cloud environment, whose branch rule admits only main."
  type        = string
  default     = "repo:weaveplatform@336817058/infraweave@1436683462:environment:objectstore-cloud"
}
