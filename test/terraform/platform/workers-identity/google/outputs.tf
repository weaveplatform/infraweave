# The values the managed-cloud-workers workflow reads as its environment's
# secrets.

output "workload_identity_provider" {
  value = google_iam_workload_identity_pool_provider.github.name
}

output "service_account" {
  value = google_service_account.ci.email
}

# The service account the worker stages' clusters run their nodes as; it
# belongs in the cluster stage variables of WORKERS_GOOGLE_INPUTS.
output "node_service_account" {
  value = google_service_account.node.email
}
