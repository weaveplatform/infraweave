terraform {
  required_version = ">= 1.16.0"

  # The lab controller supplies an owner-private state path.
  backend "local" {}

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "5.14.0"
    }
  }
}

provider "azurerm" {
  features {
    key_vault {
      # A destroyed vault is purged, so its name and keys leave nothing
      # behind for cleanup's readback to find.
      purge_soft_delete_on_destroy = true
    }
  }
  subscription_id = var.subscription_id
  # The storage account allows no shared keys, so the provider reads it
  # through Entra ID like the server does.
  storage_use_azuread = true
  # The provider's default registrations leave out these services, which a
  # subscription that has never held one has not registered.
  resource_providers_to_register = [
    "Microsoft.App",
    "Microsoft.DBforPostgreSQL",
    "Microsoft.KeyVault",
    "Microsoft.OperationalInsights",
  ]
}
