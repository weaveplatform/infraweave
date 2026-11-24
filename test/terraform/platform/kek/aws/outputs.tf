# INFRAWEAVE_TEST_AWSKMS_SIGNING_KEY, which the live test reads: the key's
# ARN, whose region the backend signs in.
output "signing_key_arn" {
  value = aws_kms_key.signing.arn
}

# INFRAWEAVE_TEST_AWSKMS_FEDERATION_KEY, which the live test signs RS256
# and PS256 with: the RSA key's ARN.
output "federation_key_arn" {
  value = aws_kms_key.federation.arn
}
