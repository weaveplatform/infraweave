# The AWS side of decision 0016's objectstore-cloud job: one test bucket
# that deletes every object a day after it is written, and two roles with
# decision 0020's grants for serve (writer) and scheduler (deleter), which
# only a job in the repository's objectstore-cloud environment can assume,
# through GitHub's OIDC token. No access key is created.

data "aws_caller_identity" "current" {}

resource "aws_iam_openid_connect_provider" "github" {
  count          = var.github_oidc_provider_arn == null ? 1 : 0
  url            = "https://token.actions.githubusercontent.com"
  client_id_list = ["sts.amazonaws.com"]
}

locals {
  oidc_provider_arn = coalesce(var.github_oidc_provider_arn, one(aws_iam_openid_connect_provider.github[*].arn))
}

resource "aws_s3_bucket" "objectstore" {
  bucket        = "infraweave-objectstore-ci-${data.aws_caller_identity.current.account_id}"
  force_destroy = true
}

resource "aws_s3_bucket_public_access_block" "objectstore" {
  bucket                  = aws_s3_bucket.objectstore.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_ownership_controls" "objectstore" {
  bucket = aws_s3_bucket.objectstore.id
  rule {
    object_ownership = "BucketOwnerEnforced"
  }
}

# Each run deletes its own prefix; this clears what a failed run leaves,
# including the parts of a multipart upload it never completed.
resource "aws_s3_bucket_lifecycle_configuration" "objectstore" {
  bucket = aws_s3_bucket.objectstore.id
  rule {
    id     = "expire-after-a-day"
    status = "Enabled"
    filter {}
    expiration {
      days = 1
    }
    abort_incomplete_multipart_upload {
      days_after_initiation = 1
    }
  }
}

# Decision 0020's grants: serve reads, writes and aborts multipart uploads;
# scheduler may also delete.
locals {
  actions = {
    writer  = ["s3:GetObject", "s3:PutObject", "s3:AbortMultipartUpload"]
    deleter = ["s3:GetObject", "s3:PutObject", "s3:AbortMultipartUpload", "s3:DeleteObject"]
  }
}

data "aws_iam_policy_document" "trust" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [local.oidc_provider_arn]
    }
    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:sub"
      values   = [var.subject]
    }
  }
}

resource "aws_iam_role" "objectstore" {
  for_each             = local.actions
  name                 = "infraweave-objectstore-ci-${each.key}"
  assume_role_policy   = data.aws_iam_policy_document.trust.json
  max_session_duration = 3600
}

data "aws_iam_policy_document" "objectstore" {
  for_each = local.actions
  statement {
    actions   = each.value
    resources = ["${aws_s3_bucket.objectstore.arn}/*"]
  }
}

resource "aws_iam_role_policy" "objectstore" {
  for_each = local.actions
  name     = "objectstore"
  role     = aws_iam_role.objectstore[each.key].id
  policy   = data.aws_iam_policy_document.objectstore[each.key].json
}
