# The dedicated Google project of the cloud tests, linked to a billing
# account, with the APIs the other platform roots use already enabled. Those
# roots apply into it by its project_id.

resource "google_project" "lab" {
  project_id      = var.project_id
  name            = var.project_name
  billing_account = var.billing_account
  org_id          = var.org_id
  folder_id       = var.folder_id
  # The default network's open firewall rules are not created; each root
  # builds the network it needs.
  auto_create_network = false
  deletion_policy     = var.delete_on_destroy ? "DELETE" : "PREVENT"
}

resource "google_project_service" "lab" {
  for_each = toset(var.services)
  project  = google_project.lab.project_id
  service  = each.value
  # Deleting the project disables everything; destroying this root without
  # deleting it leaves the APIs on for the roots still applied there.
  disable_on_destroy = false
}
