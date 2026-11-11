output "namespace" {
  description = "INFRAWEAVE_TEST_VCS_NAMESPACE"
  value       = var.organization
}

output "repository" {
  description = "INFRAWEAVE_TEST_VCS_REPOSITORY"
  value       = github_repository.lab["lab"].name
  depends_on  = [terraform_data.seed]
}

output "other" {
  description = "INFRAWEAVE_TEST_VCS_OTHER"
  value       = github_repository.lab["other"].name
  depends_on  = [terraform_data.seed]
}
