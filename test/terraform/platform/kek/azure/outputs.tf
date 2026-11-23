# INFRAWEAVE_TEST_AZUREKV_URL, which the live test reads: the key's URL
# without a version.
output "key_url" {
  value = "${azurerm_key_vault.kek.vault_uri}keys/${azurerm_key_vault_key.kek.name}"
}

# INFRAWEAVE_TEST_AZUREKV_SIGNING_KEY, which the signing live test reads:
# the signing key's ID with its version, which the backend signs with.
output "signing_key_id" {
  value = azurerm_key_vault_key.signing.id
}
