# The AWS key the awskms signing backend's live test signs tokens with
# (decision 0006): an ECC_NIST_P256 KMS key for SIGN_VERIFY. Whoever applies
# this root runs the test, and the account's key policy default lets its
# administrators sign with it. No key material leaves KMS, and nothing
# here grants an outside identity. The awskms KEK provider has no live
# test: moto serves its contract.

resource "aws_kms_key" "signing" {
  description              = "infraweave awskms signing backend live test"
  customer_master_key_spec = "ECC_NIST_P256"
  key_usage                = "SIGN_VERIFY"
  # KMS's shortest wait before a deleted key is gone.
  deletion_window_in_days = 7
}
