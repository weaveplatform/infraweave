# The values the objectstore-cloud workflow reads as its environment's secrets.

output "bucket" {
  value = aws_s3_bucket.objectstore.bucket
}

output "region" {
  value = var.region
}

output "roles" {
  value = { for k, r in aws_iam_role.objectstore : k => r.arn }
}
