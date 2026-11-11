terraform {
  required_version = ">= 1.16.0"

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "5.13.0"
    }
  }
}

provider "azurerm" {
  features {
    key_vault {
      # A destroyed vault is purged, so a later apply can take its name.
      purge_soft_delete_on_destroy = true
    }
  }
  subscription_id = var.subscription_id
  # The provider's default registrations leave out Key Vault, which a
  # subscription that has never held a vault has not registered.
  resource_providers_to_register = ["Microsoft.KeyVault"]
}
