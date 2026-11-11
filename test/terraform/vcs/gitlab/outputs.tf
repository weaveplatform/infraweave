output "host" {
  description = "INFRAWEAVE_TEST_VCS_HOST"
  value       = var.host
}

output "token" {
  description = "INFRAWEAVE_TEST_VCS_TOKEN"
  value       = gitlab_group_access_token.integration.token
  sensitive   = true
}

output "namespace" {
  description = "INFRAWEAVE_TEST_VCS_NAMESPACE"
  value       = data.gitlab_group.lab.full_path
}

output "repository" {
  description = "INFRAWEAVE_TEST_VCS_REPOSITORY"
  value       = gitlab_project.lab["lab"].path
  depends_on  = [terraform_data.seed]
}

output "other" {
  description = "INFRAWEAVE_TEST_VCS_OTHER"
  value       = gitlab_project.lab["other"].path
  depends_on  = [terraform_data.seed]
}
