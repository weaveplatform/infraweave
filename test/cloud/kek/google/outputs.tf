# INFRAWEAVE_TEST_GCPKMS_KEY, which the live test reads.
output "key_name" {
  value = google_kms_crypto_key.kek.id
}
