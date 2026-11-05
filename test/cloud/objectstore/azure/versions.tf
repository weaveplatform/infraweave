terraform {
  required_version = ">= 1.16.0"

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "5.12.0"
    }
  }
}

provider "azurerm" {
  features {}
  subscription_id = var.subscription_id
  # The storage account allows no shared keys, so the provider manages its
  # container and lifecycle through Entra ID like the job does.
  storage_use_azuread = true
}
