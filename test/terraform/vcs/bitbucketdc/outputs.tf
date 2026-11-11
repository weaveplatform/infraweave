output "host" {
  description = "INFRAWEAVE_TEST_VCS_HOST"
  value       = var.host
}

output "user" {
  description = "INFRAWEAVE_TEST_VCS_USER: git signs a project access token in as x-token-auth."
  value       = "x-token-auth"
}

output "token" {
  description = "INFRAWEAVE_TEST_VCS_TOKEN"
  value       = jsondecode(restapi_object.token.create_response).token
  sensitive   = true
}

output "namespace" {
  description = "INFRAWEAVE_TEST_VCS_NAMESPACE"
  value       = restapi_object.project.id
}

output "repository" {
  description = "INFRAWEAVE_TEST_VCS_REPOSITORY"
  value       = restapi_object.repository["lab"].id
  depends_on  = [terraform_data.seed]
}

output "other" {
  description = "INFRAWEAVE_TEST_VCS_OTHER"
  value       = restapi_object.repository["other"].id
  depends_on  = [terraform_data.seed]
}
