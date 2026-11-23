# The values the objectstore-cloud workflow reads as its environment's secrets.

output "account_url" {
  value = azurerm_storage_account.objectstore.primary_blob_endpoint
}

output "container" {
  value = azurerm_storage_container.objectstore.name
}

output "tenant_id" {
  value = azurerm_user_assigned_identity.objectstore["writer"].tenant_id
}

output "subscription_id" {
  value = var.subscription_id
}

output "client_ids" {
  value = { for k, id in azurerm_user_assigned_identity.objectstore : k => id.client_id }
}
