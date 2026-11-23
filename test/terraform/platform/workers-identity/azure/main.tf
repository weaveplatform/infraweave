# The Azure identity of the managed cloud worker batches: one user-assigned
# identity that only a job in the repository's workers-cloud environment can
# sign in as, through GitHub's OIDC token. It builds AKS clusters, their
# networks and their managed identities in one dedicated resource group,
# assigns a fixed set of roles there, and reads the quota, resource and
# cluster inventory preflight and cleanup check across the subscription.
# Nothing is registered in the tenant's directory.

locals {
  subscription = "/subscriptions/${var.subscription_id}"
}

# The identity lives apart from the group it may change, so a stage can
# neither delete it nor add a federated credential that admits another
# subject.
resource "azurerm_resource_group" "identity" {
  name     = "infraweave-workers-ci-rg"
  location = var.location
}

# The worker stages' explicit resource_group_name. AKS puts each cluster's
# nodes in its own node resource group, which AKS creates and deletes.
resource "azurerm_resource_group" "workers" {
  name     = "infraweave-workers-lab-rg"
  location = var.location
}

resource "azurerm_user_assigned_identity" "ci" {
  name                = "infraweave-workers-ci"
  resource_group_name = azurerm_resource_group.identity.name
  location            = azurerm_resource_group.identity.location
}

resource "azurerm_federated_identity_credential" "github" {
  name                      = "github-workers-cloud"
  user_assigned_identity_id = azurerm_user_assigned_identity.ci.id
  issuer                    = "https://token.actions.githubusercontent.com"
  audience                  = ["api://AzureADTokenExchange"]
  subject                   = var.subject
}

# Contributor admits every resource type, not only AKS, networks and
# managed identities: broader than ideal, bounded by a group that holds
# nothing but the worker stages' resources. It cannot assign roles.
resource "azurerm_role_assignment" "contributor" {
  scope                = azurerm_resource_group.workers.id
  role_definition_name = "Contributor"
  principal_id         = azurerm_user_assigned_identity.ci.principal_id
  principal_type       = "ServicePrincipal"
}

data "azurerm_role_definition" "assignable" {
  for_each = toset(var.assignable_roles)
  name     = each.value
  scope    = local.subscription
}

# Role Based Access Control Administrator, constrained so that it creates
# and deletes only assignments of assignable_roles to service principals,
# which managed identities are; it can never grant Owner, User Access
# Administrator or itself.
locals {
  # The condition compares bare GUIDs, never full role definition IDs.
  assignable_guids = join(", ", [for r in data.azurerm_role_definition.assignable : basename(r.role_definition_id)])
  assignment_condition = join("\nAND\n", [
    for verb, source in { write = "Request", delete = "Resource" } : trimspace(<<-EOT
    (
     (
      !(ActionMatches{'Microsoft.Authorization/roleAssignments/${verb}'})
     )
     OR
     (
      @${source}[Microsoft.Authorization/roleAssignments:RoleDefinitionId] ForAnyOfAnyValues:GuidEquals {${local.assignable_guids}}
      AND
      @${source}[Microsoft.Authorization/roleAssignments:PrincipalType] ForAnyOfAnyValues:StringEqualsIgnoreCase {'ServicePrincipal'}
     )
    )
    EOT
    )
  ])
}

resource "azurerm_role_assignment" "assign_roles" {
  scope                = azurerm_resource_group.workers.id
  role_definition_name = "Role Based Access Control Administrator"
  principal_id         = azurerm_user_assigned_identity.ci.principal_id
  principal_type       = "ServicePrincipal"
  condition_version    = "2.0"
  condition            = local.assignment_condition
}

# What cloudlab.py reads outside the worker group: regional core quota
# (az vm list-usage), the owner tag search (az resource list, az group
# list), an existing cluster (az aks show), and the AKS versions a stage
# plans against.
resource "azurerm_role_definition" "inventory" {
  name        = "infraweave workers lab reader"
  scope       = local.subscription
  description = "The quota, inventory and cluster reads of infraweave's managed cloud worker batches."

  permissions {
    actions = [
      "Microsoft.Resources/subscriptions/read",
      "Microsoft.Resources/subscriptions/resourceGroups/read",
      "Microsoft.Resources/subscriptions/resources/read",
      "Microsoft.Compute/locations/usages/read",
      "Microsoft.ContainerService/managedClusters/read",
      "Microsoft.ContainerService/locations/*/read",
    ]
  }

  assignable_scopes = [local.subscription]
}

resource "azurerm_role_assignment" "inventory" {
  scope              = local.subscription
  role_definition_id = azurerm_role_definition.inventory.role_definition_resource_id
  principal_id       = azurerm_user_assigned_identity.ci.principal_id
  principal_type     = "ServicePrincipal"
}
