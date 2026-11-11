# INFRAWEAVE_TEST_AWSKMS_SIGNING_KEY, which the live test reads: the key's
# ARN, whose region the backend signs in.
output "signing_key_arn" {
  value = aws_kms_key.signing.arn
}
