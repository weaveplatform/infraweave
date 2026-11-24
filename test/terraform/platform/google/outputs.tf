# What the acceptance runner and the worker roots read: the public origin
# every caller reaches, and the stores and keys the server uses.
output "platform" {
  value = {
    schema     = 1
    kind       = "platform"
    provider   = "google"
    owner      = var.ownership.owner
    batch      = var.ownership.batch
    expires_at = var.ownership.expires_at
    project_id = var.project_id
    region     = var.region

    # server.public_url. Its certificate is Google-managed, so every
    # caller trusts it through the system roots.
    public_url = local.public_url
    endpoints = {
      api    = "${local.public_url}/api/v1"
      worker = "${local.public_url}/worker/v1"
      state  = "${local.public_url}/state/v1"
    }
    # The A record of domain_name must hold this address before the
    # certificate provisions.
    domain_name      = var.domain_name
    load_balancer_ip = google_compute_global_address.platform.address

    service         = google_cloud_run_v2_service.platform.id
    service_account = google_service_account.platform.email
    image           = local.image
    bucket          = google_storage_bucket.objectstore.name
    kms = {
      key_ring       = google_kms_key_ring.platform.id
      kek            = google_kms_crypto_key.kek.id
      signing_key    = "${google_kms_crypto_key.signing.id}/cryptoKeyVersions/1"
      federation_key = "${google_kms_crypto_key.federation.id}/cryptoKeyVersions/1"
    }
    database = {
      instance        = google_sql_database_instance.platform.name
      connection_name = google_sql_database_instance.platform.connection_name
      private_ip      = google_sql_database_instance.platform.private_ip_address
      name            = google_sql_database.infraweave.name
      user            = google_sql_user.platform.name
    }
    network = {
      network    = google_compute_network.platform.id
      subnetwork = google_compute_subnetwork.platform.id
    }
  }
}

# Every resource the root creates, for cleanup readback. The readback
# matches an id as the suffix of a Cloud Asset Inventory name, which only
# labelled resources carry the owner label to be found by. Cloud KMS keeps
# key rings and keys after a destroy, which only destroys key versions, so
# the ring is retained beyond this owner.
output "inventory" {
  value = concat(
    [for r in [
      { kind = "network", id = google_compute_network.platform.id },
      { kind = "subnetwork", id = google_compute_subnetwork.platform.id },
      { kind = "global-address", id = google_compute_global_address.services.id },
      { kind = "service-networking-connection", id = google_service_networking_connection.services.id },
      { kind = "service-account", id = google_service_account.platform.id },
      { kind = "bucket", id = google_storage_bucket.objectstore.name },
      { kind = "custom-role", id = google_project_iam_custom_role.objectstore.id },
      { kind = "kms-crypto-key", id = google_kms_crypto_key.kek.id },
      { kind = "kms-crypto-key", id = google_kms_crypto_key.signing.id },
      { kind = "kms-crypto-key", id = google_kms_crypto_key.federation.id },
      { kind = "sql-instance", id = "projects/${var.project_id}/instances/${google_sql_database_instance.platform.name}" },
      { kind = "artifact-registry-repository", id = google_artifact_registry_repository.ghcr.id },
      { kind = "secret", id = "secrets/${google_secret_manager_secret.config.secret_id}" },
      { kind = "cloud-run-service", id = google_cloud_run_v2_service.platform.id },
      { kind = "network-endpoint-group", id = google_compute_region_network_endpoint_group.platform.id },
      { kind = "backend-service", id = google_compute_backend_service.platform.id },
      { kind = "url-map", id = google_compute_url_map.platform.id },
      { kind = "ssl-certificate", id = google_compute_managed_ssl_certificate.platform.id },
      { kind = "target-https-proxy", id = google_compute_target_https_proxy.platform.id },
      { kind = "global-address", id = google_compute_global_address.platform.id },
      { kind = "forwarding-rule", id = google_compute_global_forwarding_rule.platform.id },
      ] : merge(r, {
        scope     = "projects/${var.project_id}"
        owned     = true
        owner     = var.ownership.owner
        lifecycle = "deployment"
    })],
    [for r in google_dns_record_set.platform : {
      kind      = "dns-record"
      id        = r.id
      scope     = "projects/${var.project_id}"
      owned     = true
      owner     = var.ownership.owner
      lifecycle = "deployment"
    }],
    [{
      kind      = "kms-key-ring"
      id        = google_kms_key_ring.platform.id
      scope     = "projects/${var.project_id}"
      owned     = false
      owner     = var.ownership.owner
      lifecycle = "external"
    }],
  )
}
