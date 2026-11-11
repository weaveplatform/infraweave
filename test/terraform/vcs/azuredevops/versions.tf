terraform {
  required_version = ">= 1.16.0"

  required_providers {
    azuredevops = {
      source  = "microsoft/azuredevops"
      version = "1.16.0"
    }
  }
}

# AZDO_ORG_SERVICE_URL names the lab organization and
# AZDO_PERSONAL_ACCESS_TOKEN holds a token that creates and deletes the
# project's repositories.
provider "azuredevops" {}
