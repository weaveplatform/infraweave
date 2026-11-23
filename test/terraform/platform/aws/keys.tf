# The KMS keys: a symmetric key that wraps the keyring's data keys
# (secrets.kek.provider awskms) and an ECC_NIST_P256 key that signs the
# API's tokens (auth.signing.backend kms). Neither key's material leaves
# KMS. Each keeps the default key policy, under which the account's IAM
# policies grant its use, so the task role's policy alone admits the server.

resource "aws_kms_key" "kek" {
  description             = "infraweave lab ${local.name} KEK"
  key_usage               = "ENCRYPT_DECRYPT"
  enable_key_rotation     = true
  deletion_window_in_days = 7
}

resource "aws_kms_alias" "kek" {
  name          = "alias/${local.name}-kek"
  target_key_id = aws_kms_key.kek.key_id
}

# The awskms signing backend refuses an alias as its reference, so the
# configuration names this key by ARN; the alias only labels it.
resource "aws_kms_key" "signing" {
  description              = "infraweave lab ${local.name} token signing"
  customer_master_key_spec = "ECC_NIST_P256"
  key_usage                = "SIGN_VERIFY"
  deletion_window_in_days  = 7
}

resource "aws_kms_alias" "signing" {
  name          = "alias/${local.name}-signing"
  target_key_id = aws_kms_key.signing.key_id
}
