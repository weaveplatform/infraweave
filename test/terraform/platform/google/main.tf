# The platform scenario on Google Cloud: one infraweave platform in the
# dedicated lab project, behind public HTTPS. An external Application Load
# Balancer with a Google-managed certificate on the lab domain fronts a
# serverless NEG to a Cloud Run service running infraweave all, which
# reaches Cloud SQL for PostgreSQL by private IP over Direct VPC egress,
# signs in to it with IAM, keeps blobs in a GCS bucket and wraps and signs
# with Cloud KMS. Worker clusters on every cloud call the load balancer's
# origin, server.public_url.
#
# ref: docs/diagrams/cloud-platform.drawio, page "Platform on GCP"

locals {
  # Every name carries a short digest of the owner, so two batches in the
  # project never collide; the full owner is in the labels. A service
  # account ID allows 30 characters, which the whole owner would exceed.
  suffix = substr(sha256(var.ownership.owner), 0, 8)
  name   = "iw-platform-${local.suffix}"

  # Google label keys and values admit only lowercase letters, digits,
  # underscores and hyphens, up to 63 characters, so
  # io.infraweave.lab.owner is io_infraweave_lab_owner here, which the
  # cleanup's asset search queries.
  labels = merge(
    {
      for k, v in var.ownership.labels :
      substr(replace(lower(k), "/[^a-z0-9_-]/", "_"), 0, 63) => substr(replace(lower(v), "/[^a-z0-9_-]/", "_"), 0, 63)
    },
    {
      io_infraweave_lab_owner      = var.ownership.owner
      io_infraweave_lab_expires_at = substr(replace(lower(var.ownership.expires_at), "/[^a-z0-9_-]/", "_"), 0, 63)
    },
  )

  # Cloud KMS keeps a destroyed CryptoKey's record, labels included, for
  # good; with the owner label the cleanup's search would never read this
  # batch absent.
  kms_labels = { for k, v in local.labels : k => v if k != "io_infraweave_lab_owner" }

  public_url = "https://${var.domain_name}"

  # The remote repository serves GHCR's paths under its own host.
  image = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.ghcr.repository_id}/${trimprefix(var.image, "ghcr.io/")}"

  # Cloud SQL names a service account's IAM database user by its email
  # without the .gserviceaccount.com suffix.
  database_user = trimsuffix(google_service_account.platform.email, ".gserviceaccount.com")

  # Cloud Run routes requests to one container port, the API listener's.
  # The probes reach the admin listener on its own port.
  api_port   = 8080
  admin_port = 9090

  config_dir  = "/etc/infraweave"
  config_file = "infraweave.yaml"
}

resource "google_project_service" "apis" {
  for_each = toset(concat([
    "artifactregistry.googleapis.com",
    "cloudkms.googleapis.com",
    "compute.googleapis.com",
    "iam.googleapis.com",
    "iamcredentials.googleapis.com",
    "run.googleapis.com",
    "secretmanager.googleapis.com",
    "servicenetworking.googleapis.com",
    "sqladmin.googleapis.com",
    "storage.googleapis.com",
  ], var.dns_managed_zone == null ? [] : ["dns.googleapis.com"]))
  service = each.value
  # Other roots and batches in the project use the same APIs.
  disable_on_destroy = false
}

# Network: a subnet Cloud Run egresses into directly, and private services
# access, the peering Cloud SQL's private IP lives behind.

resource "google_compute_network" "platform" {
  name                    = local.name
  auto_create_subnetworks = false

  depends_on = [google_project_service.apis]
}

resource "google_compute_subnetwork" "platform" {
  name                     = local.name
  network                  = google_compute_network.platform.id
  ip_cidr_range            = var.subnet_cidr
  private_ip_google_access = true
}

resource "google_compute_global_address" "services" {
  name          = "${local.name}-services"
  network       = google_compute_network.platform.id
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 20
  labels        = local.labels
}

resource "google_service_networking_connection" "services" {
  network                 = google_compute_network.platform.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.services.name]
  # Cloud SQL releases the connection some time after its instance is
  # deleted, so a plain delete fails the destroy; the instance is gone by
  # then, and removing the peering lets the network go.
  deletion_policy = "REMOVE_PEERING"
}

# The runtime identity of the Cloud Run service. Each grant below is the
# least a role of infraweave all needs.

resource "google_service_account" "platform" {
  account_id   = local.name
  display_name = "infraweave platform ${var.ownership.owner}"

  depends_on = [google_project_service.apis]
}

# Keyless V4 signed URLs: the gcs driver signs through IAM signBlob as
# itself.
resource "google_service_account_iam_member" "sign_own_urls" {
  service_account_id = google_service_account.platform.name
  role               = "roles/iam.serviceAccountTokenCreator"
  member             = google_service_account.platform.member
}

# Object store: serve reads and creates objects, scheduler deletes them,
# and all runs both. GCS needs delete to overwrite, which infraweave never
# does.
#
# ref: decision 0020

resource "google_storage_bucket" "objectstore" {
  name                        = "${var.project_id}-iw-${local.suffix}"
  location                    = var.region
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
  force_destroy               = true
  labels                      = local.labels

  depends_on = [google_project_service.apis]
}

resource "google_project_iam_custom_role" "objectstore" {
  # Custom role IDs stay reserved for weeks after deletion, so each owner
  # gets its own.
  role_id     = "infraweavePlatform_${local.suffix}"
  title       = "infraweave platform object store ${local.suffix}"
  permissions = ["storage.objects.get", "storage.objects.create", "storage.objects.delete"]

  depends_on = [google_project_service.apis]
}

resource "google_storage_bucket_iam_member" "objectstore" {
  bucket = google_storage_bucket.objectstore.name
  role   = google_project_iam_custom_role.objectstore.id
  member = google_service_account.platform.member
}

# Cloud KMS: the KEK the keyring's data keys are wrapped under
# (secrets.kek.provider gcpkms) and the key api tokens are signed with
# (auth.signing.backend kms). Cloud KMS never deletes a key ring or a key,
# only key versions, so each owner gets its own ring.

resource "google_kms_key_ring" "platform" {
  name     = local.name
  location = var.region

  depends_on = [google_project_service.apis]
}

resource "google_kms_crypto_key" "kek" {
  name     = "kek"
  key_ring = google_kms_key_ring.platform.id
  purpose  = "ENCRYPT_DECRYPT"
  labels   = local.kms_labels

  version_template {
    algorithm        = "GOOGLE_SYMMETRIC_ENCRYPTION"
    protection_level = "SOFTWARE"
  }

  # Destroying the key schedules its versions for destruction after a day,
  # Cloud KMS's shortest wait.
  destroy_scheduled_duration = "86400s"
}

# The signing backend signs with a version, whose key pair is fixed; the
# first version is the one auth.signing.kms_keys names.
resource "google_kms_crypto_key" "signing" {
  name     = "signing"
  key_ring = google_kms_key_ring.platform.id
  purpose  = "ASYMMETRIC_SIGN"
  labels   = local.kms_labels

  version_template {
    algorithm        = "EC_SIGN_P256_SHA256"
    protection_level = "SOFTWARE"
  }

  destroy_scheduled_duration = "86400s"
}

resource "google_kms_crypto_key_iam_member" "kek" {
  crypto_key_id = google_kms_crypto_key.kek.id
  role          = "roles/cloudkms.cryptoKeyEncrypterDecrypter"
  member        = google_service_account.platform.member
}

# The signer reads the version's public key once at start and signs with
# AsymmetricSign; it never verifies through Cloud KMS.
resource "google_kms_crypto_key_iam_member" "signing" {
  for_each      = toset(["roles/cloudkms.signer", "roles/cloudkms.publicKeyViewer"])
  crypto_key_id = google_kms_crypto_key.signing.id
  role          = each.value
  member        = google_service_account.platform.member
}

# Cloud SQL for PostgreSQL, on a private IP only, with IAM database
# authentication. infraweave sends an OAuth token as each connection's
# password (database.iam_auth gcp) straight to the instance, without the
# Cloud SQL connector, so the instance refuses unencrypted connections and
# the URL asks for TLS.

resource "google_sql_database_instance" "platform" {
  name             = local.name
  database_version = "POSTGRES_18"
  region           = var.region
  # A lab instance is disposable.
  deletion_protection = false

  settings {
    edition                     = "ENTERPRISE"
    tier                        = var.database_tier
    availability_type           = "ZONAL"
    disk_type                   = "PD_SSD"
    disk_size                   = 10
    deletion_protection_enabled = false
    user_labels                 = local.labels

    ip_configuration {
      ipv4_enabled    = false
      private_network = google_compute_network.platform.id
      ssl_mode        = "ENCRYPTED_ONLY"
    }

    database_flags {
      name  = "cloudsql.iam_authentication"
      value = "on"
    }

    backup_configuration {
      enabled = false
    }
  }

  depends_on = [google_service_networking_connection.services]
}

resource "google_sql_database" "infraweave" {
  name     = "infraweave"
  instance = google_sql_database_instance.platform.name
  # Deleting the instance deletes the database; deleting it first fails
  # while the server's connections are open.
  deletion_policy = "ABANDON"
}

resource "google_sql_user" "platform" {
  name     = local.database_user
  instance = google_sql_database_instance.platform.name
  type     = "CLOUD_IAM_SERVICE_ACCOUNT"
  # Cloud SQL's API creates databases owned by cloudsqlsuperuser, and
  # PostgreSQL 15 and later lets only the owner create in the public
  # schema, so the migrations need the role.
  database_roles = ["cloudsqlsuperuser"]
  # The user owns the schema the migrations created, which PostgreSQL
  # refuses to drop it over; deleting the instance removes it.
  deletion_policy = "ABANDON"
}

# Cloud SQL has no instance-level IAM, so the grant is the project's,
# narrowed by a condition to this instance.
resource "google_project_iam_member" "database_login" {
  project = var.project_id
  role    = "roles/cloudsql.instanceUser"
  member  = google_service_account.platform.member

  condition {
    title      = "${local.name} only"
    expression = "resource.type == \"sqladmin.googleapis.com/Instance\" && resource.name == \"projects/${var.project_id}/instances/${google_sql_database_instance.platform.name}\""
  }
}

# Cloud Run pulls only from Artifact Registry and a few other registries,
# not GHCR, so a remote repository proxies it and caches the pinned digest.
resource "google_artifact_registry_repository" "ghcr" {
  repository_id = "iw-ghcr-${local.suffix}"
  location      = var.region
  format        = "DOCKER"
  mode          = "REMOTE_REPOSITORY"
  description   = "GHCR, for the infraweave platform image"
  labels        = local.labels

  remote_repository_config {
    description = "ghcr.io"

    docker_repository {
      custom_repository {
        uri = "https://ghcr.io"
      }
    }
  }

  depends_on = [google_project_service.apis]
}

# The server's configuration. auth.signing.kms_keys is a list of mappings,
# which no INFRAWEAVE_* variable can carry, so the whole configuration is
# one strict YAML file mounted from Secret Manager and named by --config.
# It holds no credential: the database, the bucket and the keys are all
# reached through the service account.
#
# ref: internal/app/config/config.go Load
locals {
  config = {
    server = {
      listen       = ":${local.api_port}"
      admin_listen = ":${local.admin_port}"
      public_url   = local.public_url
    }
    database = {
      url      = "postgres://${urlencode(local.database_user)}@${google_sql_database_instance.platform.private_ip_address}:5432/${google_sql_database.infraweave.name}?sslmode=require"
      iam_auth = "gcp"
    }
    objectstore = {
      driver = "gcs"
      gcs = {
        bucket = google_storage_bucket.objectstore.name
      }
    }
    secrets = {
      kek = {
        provider = "gcpkms"
        gcpkms = {
          key_name = google_kms_crypto_key.kek.id
        }
      }
    }
    auth = {
      signing = {
        backend = "kms"
        kms_keys = [{
          provider = "gcpkms"
          ref      = "${google_kms_crypto_key.signing.id}/cryptoKeyVersions/1"
        }]
      }
      operator = {
        keys = var.operator_keys
      }
    }
  }
}

resource "google_secret_manager_secret" "config" {
  secret_id = "${local.name}-config"
  labels    = local.labels

  replication {
    auto {}
  }

  depends_on = [google_project_service.apis]
}

resource "google_secret_manager_secret_version" "config" {
  secret      = google_secret_manager_secret.config.id
  secret_data = yamlencode(local.config)
}

resource "google_secret_manager_secret_iam_member" "config" {
  secret_id = google_secret_manager_secret.config.id
  role      = "roles/secretmanager.secretAccessor"
  member    = google_service_account.platform.member
}

resource "google_cloud_run_v2_service" "platform" {
  name     = local.name
  location = var.region
  labels   = local.labels
  # Only the load balancer reaches the service, and its default run.app
  # URL resolves nowhere.
  ingress              = "INGRESS_TRAFFIC_INTERNAL_LOAD_BALANCER"
  default_uri_disabled = true
  # The load balancer forwards callers unauthenticated; infraweave
  # authenticates every request itself.
  invoker_iam_disabled = true
  deletion_protection  = false

  template {
    service_account       = google_service_account.platform.email
    execution_environment = "EXECUTION_ENVIRONMENT_GEN2"
    labels                = local.labels

    # The scheduler runs in the background of every instance, between
    # requests, so the instances never scale to zero or past the two the
    # scenario tests replicas with.
    scaling {
      min_instance_count = 2
      max_instance_count = 2
    }

    vpc_access {
      egress = "PRIVATE_RANGES_ONLY"
      network_interfaces {
        network    = google_compute_network.platform.id
        subnetwork = google_compute_subnetwork.platform.id
      }
    }

    containers {
      image = local.image
      args  = ["--config", "${local.config_dir}/${local.config_file}"]

      ports {
        container_port = local.api_port
      }

      resources {
        # CPU stays allocated between requests, for the scheduler.
        cpu_idle = false
        limits = {
          cpu    = "1"
          memory = "1Gi"
        }
      }

      volume_mounts {
        name       = "config"
        mount_path = local.config_dir
      }

      # No listener binds until the migrations have run and the keyring
      # has loaded, so startup allows four minutes.
      startup_probe {
        period_seconds    = 10
        failure_threshold = 24
        timeout_seconds   = 5
        http_get {
          path = "/startupz"
          port = local.admin_port
        }
      }

      liveness_probe {
        period_seconds    = 15
        failure_threshold = 3
        timeout_seconds   = 5
        http_get {
          path = "/livez"
          port = local.admin_port
        }
      }

      # /readyz fails as shutdown begins and while PostgreSQL does not
      # answer; Cloud Run then stops routing to the instance.
      readiness_probe {
        period_seconds    = 10
        failure_threshold = 3
        timeout_seconds   = 5
        http_get {
          path = "/readyz"
          port = local.admin_port
        }
      }
    }

    volumes {
      name = "config"
      secret {
        secret = google_secret_manager_secret.config.secret_id
        items {
          # The version number, not latest, so a changed configuration
          # rolls out as a new revision.
          version = google_secret_manager_secret_version.config.version
          path    = local.config_file
        }
      }
    }
  }

  # The first revision starts with every grant in place, or its startup
  # probe fails on a denied database, key or secret.
  depends_on = [
    google_secret_manager_secret_iam_member.config,
    google_project_iam_member.database_login,
    google_sql_user.platform,
    google_kms_crypto_key_iam_member.kek,
    google_kms_crypto_key_iam_member.signing,
    google_storage_bucket_iam_member.objectstore,
    google_service_account_iam_member.sign_own_urls,
  ]
}

# The external Application Load Balancer: the public address,
# server.public_url's host, with a Google-managed certificate, in front of
# a serverless NEG to the service. A serverless NEG takes no health check;
# Cloud Run's own probes decide which instances serve.

resource "google_compute_region_network_endpoint_group" "platform" {
  name                  = local.name
  region                = var.region
  network_endpoint_type = "SERVERLESS"

  cloud_run {
    service = google_cloud_run_v2_service.platform.name
  }
}

resource "google_compute_backend_service" "platform" {
  name                  = local.name
  load_balancing_scheme = "EXTERNAL_MANAGED"

  backend {
    group = google_compute_region_network_endpoint_group.platform.id
  }
}

resource "google_compute_url_map" "platform" {
  name            = local.name
  default_service = google_compute_backend_service.platform.id
}

resource "google_compute_managed_ssl_certificate" "platform" {
  name = local.name

  managed {
    domains = [var.domain_name]
  }

  depends_on = [google_project_service.apis]
}

resource "google_compute_target_https_proxy" "platform" {
  name             = local.name
  url_map          = google_compute_url_map.platform.id
  ssl_certificates = [google_compute_managed_ssl_certificate.platform.id]
}

resource "google_compute_global_address" "platform" {
  name   = local.name
  labels = local.labels

  depends_on = [google_project_service.apis]
}

resource "google_compute_global_forwarding_rule" "platform" {
  name                  = local.name
  target                = google_compute_target_https_proxy.platform.id
  ip_address            = google_compute_global_address.platform.id
  port_range            = "443"
  load_balancing_scheme = "EXTERNAL_MANAGED"
  labels                = local.labels
}

resource "google_dns_record_set" "platform" {
  count        = var.dns_managed_zone == null ? 0 : 1
  managed_zone = var.dns_managed_zone
  name         = "${var.domain_name}."
  type         = "A"
  ttl          = 300
  rrdatas      = [google_compute_global_address.platform.address]

  depends_on = [google_project_service.apis]
}
