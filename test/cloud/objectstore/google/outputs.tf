# The values the objectstore-cloud workflow reads as repository variables.

output "bucket" {
  value = google_storage_bucket.objectstore.name
}

output "workload_identity_provider" {
  value = google_iam_workload_identity_pool_provider.github.name
}

output "service_accounts" {
  value = { for k, sa in google_service_account.objectstore : k => sa.email }
}
