# The object store: one bucket that every store of objectstore.s3 shares.
# The server expires each store's objects under its own retention, so the
# bucket has no lifecycle rule, and a destroy empties it.

resource "aws_s3_bucket" "objects" {
  bucket        = "${local.name}-${local.account_id}-${var.region}"
  force_destroy = true
}

resource "aws_s3_bucket_public_access_block" "objects" {
  bucket                  = aws_s3_bucket.objects.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_ownership_controls" "objects" {
  bucket = aws_s3_bucket.objects.id
  rule {
    object_ownership = "BucketOwnerEnforced"
  }
}

# Signed URLs and the server's own requests use HTTPS; the policy refuses
# anything else.
data "aws_iam_policy_document" "objects" {
  statement {
    sid     = "DenyInsecureTransport"
    effect  = "Deny"
    actions = ["s3:*"]
    resources = [
      aws_s3_bucket.objects.arn,
      "${aws_s3_bucket.objects.arn}/*",
    ]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    condition {
      test     = "Bool"
      variable = "aws:SecureTransport"
      values   = ["false"]
    }
  }
}

resource "aws_s3_bucket_policy" "objects" {
  bucket = aws_s3_bucket.objects.id
  policy = data.aws_iam_policy_document.objects.json

  depends_on = [aws_s3_bucket_public_access_block.objects]
}
