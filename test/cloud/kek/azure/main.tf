# The Azure keys the azurekv live tests use, which their recorded fixtures
# were recorded against: an RSA key the KEK provider wraps data keys with
# (decision 0013), and an EC key on P-256 the signing backend signs tokens
# with (decision 0006), in a vault that authorizes through Azure RBAC, with
# Key Vault Crypto Officer, which creates the keys and uses them, for
# whoever applies this root, who runs the tests. No key material leaves Key
# Vault, and nothing is registered in the tenant's directory.

data "azurerm_client_config" "current" {}

locals {
  # Vault names are global, 3 to 24 letters, digits and hyphens; a hash of
  # the subscription keeps the name stable and unique.
  vault_name = "iwkekci${substr(sha256(var.subscription_id), 0, 12)}"
}

resource "azurerm_resource_group" "kek" {
  name     = "infraweave-kek-ci-rg"
  location = var.location
}

resource "azurerm_key_vault" "kek" {
  name                       = local.vault_name
  resource_group_name        = azurerm_resource_group.kek.name
  location                   = azurerm_resource_group.kek.location
  tenant_id                  = data.azurerm_client_config.current.tenant_id
  sku_name                   = "standard"
  rbac_authorization_enabled = true
  soft_delete_retention_days = 7
  purge_protection_enabled   = false
}

resource "azurerm_role_assignment" "caller" {
  scope                = azurerm_key_vault.kek.id
  role_definition_name = "Key Vault Crypto Officer"
  principal_id         = data.azurerm_client_config.current.object_id
}

resource "azurerm_key_vault_key" "kek" {
  name         = "kek"
  key_vault_id = azurerm_key_vault.kek.id
  key_type     = "RSA"
  key_size     = 3072
  key_opts     = ["wrapKey", "unwrapKey"]

  # The data plane answers only once the role assignment has reached it.
  depends_on = [azurerm_role_assignment.caller]
}

resource "azurerm_key_vault_key" "signing" {
  name         = "signing"
  key_vault_id = azurerm_key_vault.kek.id
  key_type     = "EC"
  curve        = "P-256"
  key_opts     = ["sign", "verify"]

  depends_on = [azurerm_role_assignment.caller]
}
