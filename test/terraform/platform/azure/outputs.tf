# What the acceptance runner and the worker roots attach with. Every
# endpoint is below server.public_url, whose managed certificate Container
# Apps issues from a public CA, so ca_certificate is null: clients trust it
# with their system roots, as they do the Blob Storage endpoint signed URLs
# point at.
output "platform" {
  value = {
    schema     = 1
    kind       = "platform"
    provider   = "azure"
    owner      = var.ownership.owner
    batch      = var.ownership.batch
    expires_at = var.ownership.expires_at
    cloud = {
      tenant_id           = data.azurerm_client_config.current.tenant_id
      subscription_id     = var.subscription_id
      location            = var.location
      resource_group_name = azurerm_resource_group.platform.name
    }
    public_url     = local.public_url
    ca_certificate = null
    endpoints = {
      api      = "${local.public_url}/api/v1"
      operator = "${local.public_url}/operator/v1"
      worker   = "${local.public_url}/worker/v1"
      state    = "${local.public_url}/state/v1"
    }
    # The app's own name under the environment's domain, which answers
    # before the custom domain's certificate is bound.
    default_fqdn = azurerm_container_app.platform.ingress[0].fqdn
    # The addresses the replicas reach GHCR and the VCS providers from,
    # for an allow list on their side.
    egress_ips = azurerm_container_app.platform.outbound_ip_addresses
    storage = {
      account_url = azurerm_storage_account.platform.primary_blob_endpoint
      container   = azurerm_storage_container.platform.name
    }
    keys = {
      vault_url  = azurerm_key_vault.platform.vault_uri
      kek        = azurerm_key_vault_key.kek.versionless_id
      signing    = azurerm_key_vault_key.signing.id
      federation = azurerm_key_vault_key.federation.id
    }
    database = {
      server = azurerm_postgresql_flexible_server.platform.fqdn
      name   = azurerm_postgresql_flexible_server_database.infraweave.name
      user   = azurerm_user_assigned_identity.app.name
    }
    identity = {
      client_id    = azurerm_user_assigned_identity.app.client_id
      principal_id = azurerm_user_assigned_identity.app.principal_id
    }
    log_analytics_workspace_id = azurerm_log_analytics_workspace.platform.workspace_id
  }

  # The origin serves HTTPS only once the certificate is bound.
  depends_on = [terraform_data.certificate_binding]
}

# Every resource this root creates, and the zone it borrows, for cleanup's
# readback: an owned entry is absent once neither az resource list nor az
# group list, searching by the owner tag, returns an ID ending in its id.
# DNS records, role definitions and assignments, and the environment's
# infrastructure group carry no owner tag the search finds; the empty
# state is their evidence.
locals {
  owned = [
    { kind = "Microsoft.Resources/resourceGroups", id = azurerm_resource_group.platform.id },
    { kind = "Microsoft.Resources/resourceGroups", id = "/subscriptions/${var.subscription_id}/resourceGroups/${azurerm_container_app_environment.platform.infrastructure_resource_group_name}" },
    { kind = "Microsoft.Network/virtualNetworks", id = azurerm_virtual_network.platform.id },
    { kind = "Microsoft.Network/privateDnsZones", id = azurerm_private_dns_zone.postgres.id },
    { kind = "Microsoft.Network/privateDnsZones/virtualNetworkLinks", id = azurerm_private_dns_zone_virtual_network_link.postgres.id },
    { kind = "Microsoft.ManagedIdentity/userAssignedIdentities", id = azurerm_user_assigned_identity.app.id },
    { kind = "Microsoft.DBforPostgreSQL/flexibleServers", id = azurerm_postgresql_flexible_server.platform.id },
    { kind = "Microsoft.Storage/storageAccounts", id = azurerm_storage_account.platform.id },
    { kind = "Microsoft.KeyVault/vaults", id = azurerm_key_vault.platform.id },
    { kind = "Microsoft.OperationalInsights/workspaces", id = azurerm_log_analytics_workspace.platform.id },
    { kind = "Microsoft.App/managedEnvironments", id = azurerm_container_app_environment.platform.id },
    { kind = "Microsoft.App/managedEnvironments/managedCertificates", id = azurerm_container_app_environment_managed_certificate.platform.id },
    { kind = "Microsoft.App/containerApps", id = azurerm_container_app.platform.id },
    { kind = "Microsoft.Network/dnsZones/TXT", id = azurerm_dns_txt_record.verification.id },
    { kind = "Microsoft.Network/dnsZones/CNAME", id = azurerm_dns_cname_record.platform.id },
  ]
  authorization = concat(
    [for d in azurerm_role_definition.app : { kind = "Microsoft.Authorization/roleDefinitions", id = d.role_definition_resource_id }],
    [for a in concat(values(azurerm_role_assignment.app), [azurerm_role_assignment.delegator, azurerm_role_assignment.caller]) :
      { kind = "Microsoft.Authorization/roleAssignments", id = a.id }
    ],
  )
}

output "inventory" {
  value = concat(
    [for e in concat(local.owned, local.authorization) : merge(e, {
      scope     = "/subscriptions/${var.subscription_id}"
      owned     = true
      owner     = var.ownership.owner
      lifecycle = "deployment"
    })],
    [{
      kind      = "Microsoft.Network/dnsZones"
      id        = data.azurerm_dns_zone.lab.id
      scope     = "/subscriptions/${var.subscription_id}"
      owned     = false
      owner     = var.ownership.owner
      lifecycle = "external"
    }],
  )
}
