# INFRAWEAVE_TEST_GCPKMS_KEY, which the live test reads.
output "key_name" {
  value = google_kms_crypto_key.kek.id
}

# INFRAWEAVE_TEST_GCPKMS_SIGNING_KEY, which the signing live test reads:
# the signing key's first version.
output "signing_key" {
  value = "${google_kms_crypto_key.signing.id}/cryptoKeyVersions/1"
}
