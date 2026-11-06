# INFRAWEAVE_TEST_AZUREKV_URL, which the live test reads: the key's URL
# without a version.
output "key_url" {
  value = "${azurerm_key_vault.kek.vault_uri}keys/${azurerm_key_vault_key.kek.name}"
}
