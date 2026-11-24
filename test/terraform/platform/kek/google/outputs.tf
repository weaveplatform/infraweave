# INFRAWEAVE_TEST_GCPKMS_KEY, which the live test reads.
output "key_name" {
  value = google_kms_crypto_key.kek.id
}

# INFRAWEAVE_TEST_GCPKMS_SIGNING_KEY, which the signing live test reads:
# the signing key's first version.
output "signing_key" {
  value = "${google_kms_crypto_key.signing.id}/cryptoKeyVersions/1"
}

# INFRAWEAVE_TEST_GCPKMS_FEDERATION_PKCS1_KEY and
# INFRAWEAVE_TEST_GCPKMS_FEDERATION_PSS_KEY, which the signing live test
# signs RS256 and PS256 with: each federation key's first version.
output "federation_pkcs1_key" {
  value = "${google_kms_crypto_key.federation["pkcs1"].id}/cryptoKeyVersions/1"
}

output "federation_pss_key" {
  value = "${google_kms_crypto_key.federation["pss"].id}/cryptoKeyVersions/1"
}
