# The dedicated Azure subscription of the cloud tests, created through a
# subscription alias at a billing scope. The other platform roots apply into
# it by its subscription_id; their resource providers are registered by the
# roots that use them.

resource "azurerm_subscription" "lab" {
  alias             = var.subscription_name
  subscription_name = var.subscription_name
  billing_scope_id  = var.billing_scope_id
  workload          = var.workload
}
