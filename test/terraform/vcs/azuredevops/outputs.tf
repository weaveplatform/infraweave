output "namespace" {
  description = "INFRAWEAVE_TEST_VCS_NAMESPACE"
  value       = data.azuredevops_project.lab.name
}

output "repository" {
  description = "INFRAWEAVE_TEST_VCS_REPOSITORY"
  value       = azuredevops_git_repository.lab["lab"].name
  depends_on  = [terraform_data.seed]
}

output "other" {
  description = "INFRAWEAVE_TEST_VCS_OTHER"
  value       = azuredevops_git_repository.lab["other"].name
  depends_on  = [terraform_data.seed]
}
