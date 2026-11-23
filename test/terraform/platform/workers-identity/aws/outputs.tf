# The values the managed-cloud-workers workflow reads as its environment's
# secrets.

output "role_arn" {
  value = aws_iam_role.ci.arn
}

output "region" {
  value = var.region
}

# What the worker stages must use for every role and policy they create;
# they belong in the stage variables of WORKERS_AWS_INPUTS.
output "stage_scope" {
  value = {
    name_prefix          = var.name_prefix
    iam_path             = var.iam_path
    permissions_boundary = aws_iam_policy.boundary.arn
  }
}
