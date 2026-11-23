# The platform scenario on AWS: one infraweave platform in the dedicated lab
# account, behind public HTTPS, that the acceptance runner and the worker
# clusters of all three clouds reach at server.public_url. Two ECS Fargate
# tasks run infraweave all behind an Application Load Balancer, against RDS
# for PostgreSQL with IAM authentication, an S3 bucket, and KMS keys for the
# KEK and token signing, so the AWS drivers run end to end with no stored
# credential.
#
# network.tf holds the VPC, ingress.tf the load balancer, its certificate
# and DNS, database.tf RDS, storage.tf the bucket, keys.tf the KMS keys, and
# server.tf the server's configuration, roles and ECS service.

data "aws_caller_identity" "current" {}

data "aws_partition" "current" {}

data "aws_availability_zones" "available" {
  state = "available"
  filter {
    name   = "opt-in-status"
    values = ["opt-in-not-required"]
  }
}

locals {
  account_id = data.aws_caller_identity.current.account_id
  partition  = data.aws_partition.current.partition

  # A name derived from the owner keeps two owners' platforms in one account
  # apart, and stays within the 32 characters a load balancer or target
  # group name allows for any owner.
  name = "iwlab-${substr(sha256(var.ownership.owner), 0, 10)}"

  zones      = slice(data.aws_availability_zones.available.names, 0, 2)
  public_url = "https://${var.domain_name}"
}
