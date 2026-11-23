# The platform scenario on Azure: one infraweave platform in the dedicated
# lab subscription, behind public HTTPS, that the acceptance runner and the
# worker clusters of all three clouds attach to. A Container Apps app runs
# exactly two replicas of infraweave all in a VNet-integrated environment.
# They reach PostgreSQL Flexible Server over private access, and Blob
# Storage and Key Vault over their public endpoints, each through Entra ID
# as the app's user-assigned identity. No key, password or connection
# secret exists anywhere in the platform.

data "azurerm_client_config" "current" {}

locals {
  owner = var.ownership.owner
  # Storage account, vault and PostgreSQL server names are global, and an
  # account name is at most 24 lower-case letters and digits; a hash of
  # the owner keeps two owners' platforms apart within that.
  suffix = substr(sha256(local.owner), 0, 16)
  # The expiry travels beside the labels, so a sweep reads it from the
  # resource alone.
  tags = merge(var.ownership.labels, { "io.infraweave.lab.expires-at" = var.ownership.expires_at })

  public_url = "https://${var.domain_name}"
  record     = trimsuffix(var.domain_name, ".${var.dns_zone.name}")
  prefix     = tonumber(split("/", var.vnet_cidr)[1])
}

resource "azurerm_resource_group" "platform" {
  name     = "${local.owner}-platform"
  location = var.location
  tags     = local.tags
}

# Network: the environment's delegated subnet and PostgreSQL's, with the
# private DNS zone that resolves the server's name inside the VNet.

resource "azurerm_virtual_network" "platform" {
  name                = "infraweave"
  resource_group_name = azurerm_resource_group.platform.name
  location            = azurerm_resource_group.platform.location
  address_space       = [var.vnet_cidr]
  tags                = local.tags
}

# A workload profiles environment takes a subnet delegated to it of at
# least a /27; a /23 leaves room for its replicas through a revision
# change and for platform upgrades.
resource "azurerm_subnet" "apps" {
  name                 = "apps"
  resource_group_name  = azurerm_resource_group.platform.name
  virtual_network_name = azurerm_virtual_network.platform.name
  address_prefixes     = [cidrsubnet(var.vnet_cidr, 23 - local.prefix, 0)]

  delegation {
    name = "container-apps"
    service_delegation {
      name    = "Microsoft.App/environments"
      actions = ["Microsoft.Network/virtualNetworks/subnets/join/action"]
    }
  }
}

# The /24 after the environment's /23.
resource "azurerm_subnet" "postgres" {
  name                 = "postgres"
  resource_group_name  = azurerm_resource_group.platform.name
  virtual_network_name = azurerm_virtual_network.platform.name
  address_prefixes     = [cidrsubnet(var.vnet_cidr, 24 - local.prefix, 2)]

  delegation {
    name = "postgres"
    service_delegation {
      name    = "Microsoft.DBforPostgreSQL/flexibleServers"
      actions = ["Microsoft.Network/virtualNetworks/subnets/join/action"]
    }
  }
}

resource "azurerm_private_dns_zone" "postgres" {
  name                = "iwp-${local.suffix}.private.postgres.database.azure.com"
  resource_group_name = azurerm_resource_group.platform.name
  tags                = local.tags
}

resource "azurerm_private_dns_zone_virtual_network_link" "postgres" {
  name                 = "infraweave"
  private_dns_zone_id  = azurerm_private_dns_zone.postgres.id
  virtual_network_id   = azurerm_virtual_network.platform.id
  registration_enabled = false
  tags                 = local.tags
}

# The app's identity, the only principal the server signs in as.
resource "azurerm_user_assigned_identity" "app" {
  name                = "infraweave-${local.suffix}"
  resource_group_name = azurerm_resource_group.platform.name
  location            = azurerm_resource_group.platform.location
  tags                = local.tags
}

# PostgreSQL: Entra authentication only, reachable only from the VNet.

resource "azurerm_postgresql_flexible_server" "platform" {
  name                          = "iwp-${local.suffix}"
  resource_group_name           = azurerm_resource_group.platform.name
  location                      = azurerm_resource_group.platform.location
  version                       = "18"
  sku_name                      = var.postgres_sku
  storage_mb                    = 32768
  storage_tier                  = "P4"
  backup_retention_days         = 7
  geo_redundant_backup_enabled  = false
  delegated_subnet_id           = azurerm_subnet.postgres.id
  private_dns_zone_id           = azurerm_private_dns_zone.postgres.id
  public_network_access_enabled = false
  tags                          = local.tags

  authentication {
    active_directory_auth_enabled = true
    password_auth_enabled         = false
    tenant_id                     = data.azurerm_client_config.current.tenant_id
  }

  # Azure picks the zone, and Terraform would otherwise plan to clear it.
  lifecycle {
    ignore_changes = [zone]
  }

  depends_on = [azurerm_private_dns_zone_virtual_network_link.postgres]
}

# The app's identity is the server's Entra administrator: creating a
# narrower Entra role takes SQL from inside the VNet as an administrator,
# which no step of this root can run. The administrator is a member of
# azure_pg_admin, not a superuser, and it is the role database.url names.
resource "azurerm_postgresql_flexible_server_active_directory_administrator" "app" {
  server_name         = azurerm_postgresql_flexible_server.platform.name
  resource_group_name = azurerm_resource_group.platform.name
  tenant_id           = data.azurerm_client_config.current.tenant_id
  object_id           = azurerm_user_assigned_identity.app.principal_id
  principal_name      = azurerm_user_assigned_identity.app.name
  principal_type      = "ServicePrincipal"
}

resource "azurerm_postgresql_flexible_server_database" "infraweave" {
  name      = "infraweave"
  server_id = azurerm_postgresql_flexible_server.platform.id
  charset   = "UTF8"
  collation = "en_US.utf8"
}

# Blob Storage: shared keys off, so every request and every signed URL
# goes through Entra ID; worker Pods on other clouds fetch the signed URLs
# from its public endpoint.

resource "azurerm_storage_account" "platform" {
  name                            = "iwp${local.suffix}"
  resource_group_name             = azurerm_resource_group.platform.name
  location                        = azurerm_resource_group.platform.location
  account_kind                    = "StorageV2"
  account_tier                    = "Standard"
  account_replication_type        = "LRS"
  min_tls_version                 = "TLS1_2"
  https_traffic_only_enabled      = true
  shared_access_key_enabled       = false
  allow_nested_items_to_be_public = false
  default_to_oauth_authentication = true
  tags                            = local.tags
}

resource "azurerm_storage_container" "platform" {
  name                  = "infraweave"
  storage_account_id    = azurerm_storage_account.platform.id
  container_access_type = "private"
}

# Key Vault: the KEK the keyring's data keys are wrapped under, and the
# key API tokens are signed with. No key material leaves the vault.

resource "azurerm_key_vault" "platform" {
  name                       = "iwp-${local.suffix}"
  resource_group_name        = azurerm_resource_group.platform.name
  location                   = azurerm_resource_group.platform.location
  tenant_id                  = data.azurerm_client_config.current.tenant_id
  sku_name                   = "standard"
  rbac_authorization_enabled = true
  soft_delete_retention_days = 7
  purge_protection_enabled   = false
  tags                       = local.tags
}

# Whoever applies the root creates the keys.
resource "azurerm_role_assignment" "caller" {
  scope                = azurerm_key_vault.platform.id
  role_definition_name = "Key Vault Crypto Officer"
  principal_id         = data.azurerm_client_config.current.object_id
}

# The azurekv KEK provider wraps with RSA-OAEP-256.
resource "azurerm_key_vault_key" "kek" {
  name         = "kek"
  key_vault_id = azurerm_key_vault.platform.id
  key_type     = "RSA"
  key_size     = 3072
  key_opts     = ["wrapKey", "unwrapKey"]
  tags         = local.tags

  # The data plane answers only once the role assignment has reached it.
  depends_on = [azurerm_role_assignment.caller]
}

# The azurekv signing backend signs with an EC key on P-256.
resource "azurerm_key_vault_key" "signing" {
  name         = "signing"
  key_vault_id = azurerm_key_vault.platform.id
  key_type     = "EC"
  curve        = "P-256"
  key_opts     = ["sign", "verify"]
  tags         = local.tags

  depends_on = [azurerm_role_assignment.caller]
}

# The app's grants, each the calls the server makes and no more, assigned
# at the narrowest scope that holds them: blobs are read, written and
# deleted in the one container (serve writes, the scheduler deletes, and
# all is both); the KEK only wraps and unwraps; the signing key is read
# for its public half and signs. Each role is defined on its resource,
# where it alone is assignable, and is named for the owner, since custom
# role names are unique across the tenant.
locals {
  blobs = "Microsoft.Storage/storageAccounts/blobServices/containers/blobs"
  keys  = "Microsoft.KeyVault/vaults/keys"
  grants = {
    objects = {
      defined_on   = azurerm_storage_account.platform.id
      scope        = azurerm_storage_container.platform.id
      data_actions = ["${local.blobs}/read", "${local.blobs}/write", "${local.blobs}/add/action", "${local.blobs}/delete"]
    }
    kek = {
      defined_on   = azurerm_key_vault.platform.id
      scope        = azurerm_key_vault_key.kek.resource_versionless_id
      data_actions = ["${local.keys}/wrap/action", "${local.keys}/unwrap/action"]
    }
    signing = {
      defined_on   = azurerm_key_vault.platform.id
      scope        = azurerm_key_vault_key.signing.resource_versionless_id
      data_actions = ["${local.keys}/read", "${local.keys}/sign/action"]
    }
  }
}

resource "azurerm_role_definition" "app" {
  for_each    = local.grants
  name        = "infraweave platform ${each.key} ${local.owner}"
  scope       = each.value.defined_on
  description = "The ${each.key} grants of the infraweave platform scenario owned by ${local.owner}."

  permissions {
    data_actions = each.value.data_actions
  }

  assignable_scopes = [each.value.defined_on]
}

resource "azurerm_role_assignment" "app" {
  for_each           = local.grants
  scope              = each.value.scope
  role_definition_id = azurerm_role_definition.app[each.key].role_definition_resource_id
  principal_id       = azurerm_user_assigned_identity.app.principal_id
  principal_type     = "ServicePrincipal"
}

# A user delegation key signs the object store's URLs; Storage Blob
# Delegator grants getting one and nothing else, and only on the account.
resource "azurerm_role_assignment" "delegator" {
  scope                = azurerm_storage_account.platform.id
  role_definition_name = "Storage Blob Delegator"
  principal_id         = azurerm_user_assigned_identity.app.principal_id
  principal_type       = "ServicePrincipal"
}

# Container Apps: the replicas' logs are the server's only diagnostics, so
# the environment sends them to a workspace the destroy removes with it.

resource "azurerm_log_analytics_workspace" "platform" {
  name                = "infraweave"
  resource_group_name = azurerm_resource_group.platform.name
  location            = azurerm_resource_group.platform.location
  sku                 = "PerGB2018"
  retention_in_days   = 30
  tags                = local.tags
}

resource "azurerm_container_app_environment" "platform" {
  name                           = "infraweave"
  resource_group_name            = azurerm_resource_group.platform.name
  location                       = azurerm_resource_group.platform.location
  infrastructure_subnet_id       = azurerm_subnet.apps.id
  internal_load_balancer_enabled = false
  # Container Apps creates and deletes this group, which holds the
  # environment's load balancer and public address; naming it puts its ID
  # in the inventory.
  infrastructure_resource_group_name = "${local.owner}-platform-infrastructure"
  logs_destination                   = "log-analytics"
  log_analytics_workspace_id         = azurerm_log_analytics_workspace.platform.id
  tags                               = local.tags

  workload_profile {
    name                  = "Consumption"
    workload_profile_type = "Consumption"
  }
}

# The server's configuration, the strict YAML file --config names. Every
# key is one internal/app/config.Load decodes; auth.signing.kms_keys is a
# list of objects, which no INFRAWEAVE_* variable can carry, so the whole
# configuration is a file rather than the environment.
locals {
  # The listeners: ingress targets the API's, and the probes the admin
  # listener's, which ingress never exposes.
  api_port   = 8080
  admin_port = 9090
  config_dir = "/etc/infraweave"

  # The user is the Entra administrator's role; iam_auth azure sends a
  # token as its password, which needs TLS, verified here against the
  # image's system roots, which hold the server certificate's CA.
  database_url = format(
    "postgres://%s@%s:5432/%s?sslmode=verify-full&sslrootcert=system",
    azurerm_user_assigned_identity.app.name,
    azurerm_postgresql_flexible_server.platform.fqdn,
    azurerm_postgresql_flexible_server_database.infraweave.name,
  )

  config = yamlencode({
    server = {
      listen       = ":${local.api_port}"
      admin_listen = ":${local.admin_port}"
      public_url   = local.public_url
    }
    log = {
      level = var.log_level
    }
    database = {
      url      = local.database_url
      iam_auth = "azure"
    }
    objectstore = {
      driver = "azureblob"
      azureblob = {
        account_url = azurerm_storage_account.platform.primary_blob_endpoint
        container   = azurerm_storage_container.platform.name
      }
    }
    secrets = {
      kek = {
        provider = "azurekv"
        azurekv = {
          vault_url = azurerm_key_vault.platform.vault_uri
          key_name  = azurerm_key_vault_key.kek.name
        }
      }
    }
    auth = {
      signing = {
        backend = "kms"
        # The signing backend takes a key ID with its version.
        kms_keys = [{ provider = "azurekv", ref = azurerm_key_vault_key.signing.id }]
      }
      operator = {
        keys = var.operator_keys
      }
    }
  })
}

resource "azurerm_container_app" "platform" {
  name                         = "infraweave"
  resource_group_name          = azurerm_resource_group.platform.name
  container_app_environment_id = azurerm_container_app_environment.platform.id
  revision_mode                = "Single"
  workload_profile_name        = "Consumption"
  tags                         = local.tags

  identity {
    type         = "UserAssigned"
    identity_ids = [azurerm_user_assigned_identity.app.id]
  }

  # A Secret volume is the only way Container Apps mounts a file; the
  # configuration holds no secret.
  secret {
    name  = "config"
    value = local.config
  }

  ingress {
    external_enabled           = true
    target_port                = local.api_port
    transport                  = "http"
    allow_insecure_connections = false

    traffic_weight {
      latest_revision = true
      percentage      = 100
    }
  }

  template {
    # Two replicas, never scaled: the scenarios count on both serving and
    # both running the scheduler.
    min_replicas = 2
    max_replicas = 2
    # server.shutdown.ready_delay and server.shutdown.timeout, 5 and 30
    # seconds by default, with room for the process to exit.
    termination_grace_period_seconds = 45

    volume {
      name         = "config"
      storage_type = "Secret"
    }

    container {
      name   = "infraweave"
      image  = var.image
      cpu    = var.cpu
      memory = var.memory
      # The image's entrypoint is infraweave all. Each secret is a file
      # named for it in the volume.
      args = ["--config", "${local.config_dir}/config"]

      volume_mounts {
        name = "config"
        path = local.config_dir
      }

      # azidentity's default chain picks the user-assigned identity by
      # its client ID, for PostgreSQL, Blob Storage and Key Vault alike.
      env {
        name  = "AZURE_CLIENT_ID"
        value = azurerm_user_assigned_identity.app.client_id
      }

      # A changed secret alone starts no new revision, and the replicas
      # read the file only at start; the digest makes a changed
      # configuration a changed template.
      env {
        name  = "CONFIG_SHA256"
        value = sha256(local.config)
      }

      # The admin listener binds only after migrations and the keyring,
      # so the startup probe allows five minutes.
      startup_probe {
        transport               = "HTTP"
        port                    = local.admin_port
        path                    = "/startupz"
        interval_seconds        = 5
        timeout                 = 3
        failure_count_threshold = 60
      }

      readiness_probe {
        transport               = "HTTP"
        port                    = local.admin_port
        path                    = "/readyz"
        interval_seconds        = 5
        timeout                 = 3
        failure_count_threshold = 3
        success_count_threshold = 1
      }

      liveness_probe {
        transport               = "HTTP"
        port                    = local.admin_port
        path                    = "/livez"
        interval_seconds        = 10
        timeout                 = 3
        failure_count_threshold = 3
      }
    }
  }

  # A replica that starts before its grants or its database role exist
  # fails its start and restarts until they do.
  depends_on = [
    azurerm_role_assignment.app,
    azurerm_role_assignment.delegator,
    azurerm_postgresql_flexible_server_active_directory_administrator.app,
  ]
}

# The public origin: domain_name in the borrowed zone, verified by its
# asuid TXT record, pointed at the app, and served with a certificate
# Container Apps issues and renews.

data "azurerm_dns_zone" "lab" {
  name                = var.dns_zone.name
  resource_group_name = var.dns_zone.resource_group_name
}

resource "azurerm_dns_txt_record" "verification" {
  name                = "asuid.${local.record}"
  zone_name           = data.azurerm_dns_zone.lab.name
  resource_group_name = data.azurerm_dns_zone.lab.resource_group_name
  ttl                 = 300
  tags                = local.tags

  record {
    value = azurerm_container_app.platform.custom_domain_verification_id
  }
}

resource "azurerm_dns_cname_record" "platform" {
  name                = local.record
  zone_name           = data.azurerm_dns_zone.lab.name
  resource_group_name = data.azurerm_dns_zone.lab.resource_group_name
  ttl                 = 300
  record              = azurerm_container_app.platform.ingress[0].fqdn
  tags                = local.tags
}

# The hostname joins the app unbound; the managed certificate validates
# through it and the CNAME, and terraform_data.certificate_binding binds
# them, outside what this resource tracks.
resource "azurerm_container_app_custom_domain" "platform" {
  name             = var.domain_name
  container_app_id = azurerm_container_app.platform.id

  lifecycle {
    ignore_changes = [certificate_binding_type, container_app_environment_certificate_id]
  }

  depends_on = [azurerm_dns_txt_record.verification, azurerm_dns_cname_record.platform]
}

resource "azurerm_container_app_environment_managed_certificate" "platform" {
  name                         = "infraweave"
  container_app_environment_id = azurerm_container_app_environment.platform.id
  subject_name                 = var.domain_name
  domain_control_validation    = "CNAME"
  tags                         = local.tags

  depends_on = [azurerm_container_app_custom_domain.platform]
}

# azurerm_container_app_custom_domain binds only certificates uploaded to
# the environment, never a managed one, so the Azure CLI binds it; the
# applying shell must be signed in with az. Destroy unbinds it first,
# since Container Apps refuses to delete a certificate a hostname uses;
# the hostname's own resource then finds nothing to remove.
resource "terraform_data" "certificate_binding" {
  triggers_replace = [
    azurerm_container_app_custom_domain.platform.id,
    azurerm_container_app_environment_managed_certificate.platform.id,
  ]

  input = {
    subscription   = var.subscription_id
    resource_group = azurerm_resource_group.platform.name
    app            = azurerm_container_app.platform.name
    domain         = azurerm_container_app_custom_domain.platform.name
    certificate    = azurerm_container_app_environment_managed_certificate.platform.id
  }

  provisioner "local-exec" {
    command = "az containerapp hostname bind --subscription \"$SUBSCRIPTION\" --resource-group \"$RESOURCE_GROUP\" --name \"$APP\" --hostname \"$DOMAIN\" --certificate \"$CERTIFICATE\" --output none"
    environment = {
      SUBSCRIPTION   = self.input.subscription
      RESOURCE_GROUP = self.input.resource_group
      APP            = self.input.app
      DOMAIN         = self.input.domain
      CERTIFICATE    = self.input.certificate
    }
  }

  # A hostname the app no longer lists, or an app already gone, leaves
  # nothing to unbind; a failed sign-in surfaces as the certificate's
  # failed delete.
  provisioner "local-exec" {
    when    = destroy
    command = "if az containerapp hostname list --subscription \"$SUBSCRIPTION\" --resource-group \"$RESOURCE_GROUP\" --name \"$APP\" --query \"[?name=='$DOMAIN'].name\" --output tsv | grep -qx \"$DOMAIN\"; then az containerapp hostname delete --subscription \"$SUBSCRIPTION\" --resource-group \"$RESOURCE_GROUP\" --name \"$APP\" --hostname \"$DOMAIN\" --yes --output none; fi"
    environment = {
      SUBSCRIPTION   = self.input.subscription
      RESOURCE_GROUP = self.input.resource_group
      APP            = self.input.app
      DOMAIN         = self.input.domain
    }
  }
}
