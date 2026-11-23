# The subscription_id the objectstore, kek and workers-identity roots take.
output "subscription_id" {
  value = azurerm_subscription.lab.subscription_id
}

output "tenant_id" {
  value = azurerm_subscription.lab.tenant_id
}
