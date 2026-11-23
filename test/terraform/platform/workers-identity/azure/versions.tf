terraform {
  required_version = ">= 1.16.0"

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "5.14.0"
    }
  }
}

provider "azurerm" {
  features {}
  subscription_id = var.subscription_id
  # The worker identity cannot register resource providers, so the stages
  # register none and applying this root registers the ones they use.
  resource_providers_to_register = [
    "Microsoft.Compute",
    "Microsoft.ContainerService",
    "Microsoft.ManagedIdentity",
    "Microsoft.Network",
  ]
}
