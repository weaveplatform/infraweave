# The Google Cloud identity of the managed cloud worker batches: one service
# account that only the managed-cloud-workers workflow on main, in the
# repository's workers-cloud environment, can impersonate, through GitHub's
# OIDC token. It builds GKE clusters and their networks in the lab project,
# runs their nodes as one node service account, and reads the quota and
# asset inventory preflight and cleanup check. No key is created.

locals {
  # The token's workflow_ref claim names the workflow file and the ref it
  # ran from, so a pull request or another workflow never matches.
  workflow_ref = "${var.repository}/${var.workflow}@refs/heads/main"
}

resource "google_project_service" "apis" {
  for_each = toset([
    "cloudasset.googleapis.com",
    "cloudresourcemanager.googleapis.com",
    "compute.googleapis.com",
    "container.googleapis.com",
    "iam.googleapis.com",
    "iamcredentials.googleapis.com",
    "sts.googleapis.com",
  ])
  service            = each.value
  disable_on_destroy = false
}

resource "google_iam_workload_identity_pool" "github" {
  workload_identity_pool_id = "infraweave-workers"
  display_name              = "infraweave workers"
  description               = "The infraweave managed-cloud-workers workflow on main"

  depends_on = [google_project_service.apis]
}

resource "google_iam_workload_identity_pool_provider" "github" {
  workload_identity_pool_id          = google_iam_workload_identity_pool.github.workload_identity_pool_id
  workload_identity_pool_provider_id = "infraweave"
  display_name                       = "infraweave"

  attribute_mapping = {
    "google.subject"          = "assertion.sub"
    "attribute.repository_id" = "assertion.repository_id"
    "attribute.environment"   = "assertion.environment"
    "attribute.workflow_ref"  = "assertion.workflow_ref"
  }
  attribute_condition = join(" && ", [
    "assertion.repository_id == '${var.repository_id}'",
    "assertion.environment == '${var.environment}'",
    "assertion.workflow_ref == '${local.workflow_ref}'",
  ])

  oidc {
    issuer_uri = "https://token.actions.githubusercontent.com"
  }
}

resource "google_service_account" "ci" {
  account_id   = "infraweave-workers-ci"
  display_name = "infraweave managed cloud workers"

  depends_on = [google_project_service.apis]
}

resource "google_service_account_iam_member" "federation" {
  service_account_id = google_service_account.ci.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principal://iam.googleapis.com/${google_iam_workload_identity_pool.github.name}/subject/${var.subject}"
}

# Project-wide roles, broader than ideal because IAM Conditions cannot
# narrow GKE clusters or VPC networks by name. container.admin is what
# installing the worker chart's CRDs and cluster roles needs inside a
# cluster; it also reaches every other cluster in the project, which is
# why the project holds nothing else. compute.networkAdmin covers networks,
# subnetworks, routers and Cloud NAT and the region quota preflight reads,
# but no firewall rules; GKE creates the ones a cluster needs itself.
resource "google_project_iam_member" "ci" {
  for_each = toset([
    "roles/container.admin",
    "roles/compute.networkAdmin",
    "roles/cloudasset.viewer",
    "roles/serviceusage.serviceUsageConsumer",
  ])
  project = var.project_id
  role    = each.value
  member  = google_service_account.ci.member
}

# The stages' clusters run their nodes as this account rather than the
# Compute Engine default, which holds Editor. The service account may act
# as it and as no other: IAM Conditions do not recognize service account
# names, so a stage cannot be allowed to create and bind service accounts
# without being allowed to bind every one in the project.
resource "google_service_account" "node" {
  account_id   = "infraweave-workers-node"
  display_name = "infraweave managed cloud worker nodes"

  depends_on = [google_project_service.apis]
}

resource "google_project_iam_member" "node" {
  project = var.project_id
  role    = "roles/container.defaultNodeServiceAccount"
  member  = google_service_account.node.member
}

resource "google_service_account_iam_member" "act_as_node" {
  service_account_id = google_service_account.node.name
  role               = "roles/iam.serviceAccountUser"
  member             = google_service_account.ci.member
}

# Workload Identity Federation for GKE grants project roles directly to a
# Kubernetes service account's principal; this lets a stage grant and
# revoke grantable_roles, and no other role, on the project.
# ref: https://cloud.google.com/iam/docs/setting-limits-on-granting-roles
resource "google_project_iam_member" "grant_workload_roles" {
  count   = length(var.grantable_roles) > 0 ? 1 : 0
  project = var.project_id
  role    = "roles/resourcemanager.projectIamAdmin"
  member  = google_service_account.ci.member

  condition {
    title      = "worker workload roles only"
    expression = "api.getAttribute('iam.googleapis.com/modifiedGrantsByRole', []).hasOnly(${jsonencode(var.grantable_roles)})"
  }
}
