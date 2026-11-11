output "namespace" {
  description = "INFRAWEAVE_TEST_VCS_NAMESPACE"
  value       = var.workspace
}

output "repository" {
  description = "INFRAWEAVE_TEST_VCS_REPOSITORY"
  value       = bitbucket_repository.lab["lab"].slug
  depends_on  = [terraform_data.seed]
}

output "other" {
  description = "INFRAWEAVE_TEST_VCS_OTHER"
  value       = bitbucket_repository.lab["other"].slug
  depends_on  = [terraform_data.seed]
}
