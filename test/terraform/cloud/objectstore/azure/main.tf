# The Azure side of decision 0016's objectstore-cloud job: one storage
# account with no shared keys whose blobs are deleted a day after they are
# written, and two user-assigned identities with decision 0020's grants for
# serve (writer) and scheduler (deleter), which only a job in the
# repository's objectstore-cloud environment can sign in as, through
# GitHub's OIDC token. Nothing is registered in the tenant's directory.

locals {
  # Storage account names are global, 3 to 24 lowercase letters and digits;
  # a hash of the subscription keeps the name stable and unique.
  account_name = "iwobjci${substr(sha256(var.subscription_id), 0, 12)}"
  blobs        = "Microsoft.Storage/storageAccounts/blobServices/containers/blobs"
}

resource "azurerm_resource_group" "objectstore" {
  name     = "infraweave-objectstore-ci-rg"
  location = var.location
}

resource "azurerm_storage_account" "objectstore" {
  name                            = local.account_name
  resource_group_name             = azurerm_resource_group.objectstore.name
  location                        = azurerm_resource_group.objectstore.location
  account_kind                    = "StorageV2"
  account_tier                    = "Standard"
  account_replication_type        = "LRS"
  min_tls_version                 = "TLS1_2"
  https_traffic_only_enabled      = true
  shared_access_key_enabled       = false
  allow_nested_items_to_be_public = false
  default_to_oauth_authentication = true
}

resource "azurerm_storage_container" "objectstore" {
  name                  = "objectstore"
  storage_account_id    = azurerm_storage_account.objectstore.id
  container_access_type = "private"
}

# Each run deletes its own prefix; this clears what a failed run leaves.
resource "azurerm_storage_management_policy" "objectstore" {
  storage_account_id = azurerm_storage_account.objectstore.id
  rule {
    name    = "expire-after-a-day"
    enabled = true
    filters {
      blob_types = ["blockBlob"]
    }
    actions {
      base_blob {
        delete_after_days_since_creation_greater_than = 1
      }
    }
  }
}

# Decision 0020's grants: serve reads and writes blobs and gets a user
# delegation key to sign SAS URLs; scheduler may also delete.
locals {
  data_actions = {
    writer  = ["${local.blobs}/read", "${local.blobs}/write", "${local.blobs}/add/action"]
    deleter = ["${local.blobs}/read", "${local.blobs}/write", "${local.blobs}/add/action", "${local.blobs}/delete"]
  }
}

resource "azurerm_role_definition" "objectstore" {
  for_each    = local.data_actions
  name        = "infraweave object store ${each.key}"
  scope       = azurerm_storage_account.objectstore.id
  description = "Decision 0020's object store grants for the ${each.key} identity of infraweave's objectstore-cloud job."

  permissions {
    actions      = ["Microsoft.Storage/storageAccounts/blobServices/generateUserDelegationKey/action"]
    data_actions = each.value
  }

  assignable_scopes = [azurerm_storage_account.objectstore.id]
}

resource "azurerm_user_assigned_identity" "objectstore" {
  for_each            = local.data_actions
  name                = "infraweave-objectstore-ci-${each.key}"
  resource_group_name = azurerm_resource_group.objectstore.name
  location            = azurerm_resource_group.objectstore.location
}

resource "azurerm_federated_identity_credential" "github" {
  for_each                  = local.data_actions
  name                      = "github-objectstore-cloud"
  user_assigned_identity_id = azurerm_user_assigned_identity.objectstore[each.key].id
  issuer                    = "https://token.actions.githubusercontent.com"
  audience                  = ["api://AzureADTokenExchange"]
  subject                   = var.subject
}

resource "azurerm_role_assignment" "objectstore" {
  for_each           = local.data_actions
  scope              = azurerm_storage_account.objectstore.id
  role_definition_id = azurerm_role_definition.objectstore[each.key].role_definition_resource_id
  principal_id       = azurerm_user_assigned_identity.objectstore[each.key].principal_id
  principal_type     = "ServicePrincipal"
}
