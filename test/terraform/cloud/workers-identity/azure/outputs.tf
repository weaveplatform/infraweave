# The values the managed-cloud-workers workflow reads as its environment's
# secrets.

output "client_id" {
  value = azurerm_user_assigned_identity.ci.client_id
}

output "tenant_id" {
  value = azurerm_user_assigned_identity.ci.tenant_id
}

output "subscription_id" {
  value = var.subscription_id
}

# The worker stages' resource_group_name and location identifiers in
# WORKERS_AZURE_INPUTS.
output "resource_group_name" {
  value = azurerm_resource_group.workers.name
}

output "location" {
  value = azurerm_resource_group.workers.location
}
