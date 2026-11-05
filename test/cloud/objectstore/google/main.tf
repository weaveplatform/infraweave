# The Google Cloud side of decision 0016's objectstore-cloud job: one test
# bucket that deletes every object a day after it is written, and two
# service accounts with decision 0020's grants for serve (writer) and
# scheduler (deleter), which only the job's workflow on main can
# impersonate, through GitHub's OIDC token. No key is created.

locals {
  # The token's workflow_ref claim names the workflow file and the ref it
  # ran from, so a pull request or another workflow never matches.
  workflow_ref = "${var.repository}/${var.workflow}@refs/heads/main"
}

resource "google_project_service" "apis" {
  for_each = toset([
    "iam.googleapis.com",
    "iamcredentials.googleapis.com",
    "sts.googleapis.com",
    "storage.googleapis.com",
  ])
  service            = each.value
  disable_on_destroy = false
}

resource "google_storage_bucket" "objectstore" {
  name                        = "${var.project_id}-objectstore-ci"
  location                    = var.location
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"
  force_destroy               = true

  # Each run deletes its own prefix; this clears what a failed run leaves.
  lifecycle_rule {
    condition {
      age = 1
    }
    action {
      type = "Delete"
    }
  }

  depends_on = [google_project_service.apis]
}

resource "google_iam_workload_identity_pool" "github" {
  workload_identity_pool_id = "infraweave-ci"
  display_name              = "infraweave CI"
  description               = "The infraweave objectstore-cloud workflow on main"

  depends_on = [google_project_service.apis]
}

resource "google_iam_workload_identity_pool_provider" "github" {
  workload_identity_pool_id          = google_iam_workload_identity_pool.github.workload_identity_pool_id
  workload_identity_pool_provider_id = "infraweave"
  display_name                       = "infraweave"

  attribute_mapping = {
    "google.subject"          = "assertion.sub"
    "attribute.repository_id" = "assertion.repository_id"
    "attribute.workflow_ref"  = "assertion.workflow_ref"
  }
  attribute_condition = "assertion.repository_id == '${var.repository_id}' && assertion.workflow_ref == '${local.workflow_ref}'"

  oidc {
    issuer_uri = "https://token.actions.githubusercontent.com"
  }
}

# Decision 0020's grants: serve reads and creates objects and signs its own
# URLs; scheduler may also delete. GCS needs delete to overwrite, and
# infraweave never overwrites, so the writer's lack of it is the check.
resource "google_project_iam_custom_role" "writer" {
  role_id     = "infraweaveObjectstoreWriter"
  title       = "infraweave object store writer"
  permissions = ["storage.objects.get", "storage.objects.create"]
}

resource "google_project_iam_custom_role" "deleter" {
  role_id     = "infraweaveObjectstoreDeleter"
  title       = "infraweave object store deleter"
  permissions = ["storage.objects.get", "storage.objects.create", "storage.objects.delete"]
}

locals {
  identities = {
    writer  = google_project_iam_custom_role.writer.id
    deleter = google_project_iam_custom_role.deleter.id
  }
}

resource "google_service_account" "objectstore" {
  for_each     = local.identities
  account_id   = "objectstore-ci-${each.key}"
  display_name = "infraweave objectstore-cloud ${each.key}"
}

resource "google_storage_bucket_iam_member" "objectstore" {
  for_each = local.identities
  bucket   = google_storage_bucket.objectstore.name
  role     = each.value
  member   = google_service_account.objectstore[each.key].member
}

# Keyless V4 signed URLs: the driver signs through IAM signBlob as itself.
resource "google_service_account_iam_member" "sign_own_urls" {
  for_each           = local.identities
  service_account_id = google_service_account.objectstore[each.key].name
  role               = "roles/iam.serviceAccountTokenCreator"
  member             = google_service_account.objectstore[each.key].member
}

resource "google_service_account_iam_member" "federation" {
  for_each           = local.identities
  service_account_id = google_service_account.objectstore[each.key].name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.github.name}/attribute.workflow_ref/${local.workflow_ref}"
}
