# The Google Cloud keys the gcpkms live tests use: the key the KEK provider
# wraps data keys with (decision 0013), and the key the signing backend
# signs tokens with (decision 0006), which their recorded fixtures were
# recorded against. Whoever runs the tests needs Cloud KMS CryptoKey
# Encrypter/Decrypter on the first and Signer/Verifier on the second; the
# project's owners have both already. No key material leaves Cloud KMS,
# and nothing here grants an outside identity.

resource "google_project_service" "kms" {
  service            = "cloudkms.googleapis.com"
  disable_on_destroy = false
}

# Cloud KMS never deletes a key ring, so its name is fixed: a later apply
# adopts it rather than failing on a second one.
resource "google_kms_key_ring" "kek" {
  name     = "infraweave-ci"
  location = var.location

  depends_on = [google_project_service.kms]
}

resource "google_kms_crypto_key" "kek" {
  name     = "kek"
  key_ring = google_kms_key_ring.kek.id
  purpose  = "ENCRYPT_DECRYPT"

  version_template {
    algorithm        = "GOOGLE_SYMMETRIC_ENCRYPTION"
    protection_level = "SOFTWARE"
  }

  # Destroying the key schedules its versions for destruction after a day,
  # Cloud KMS's shortest wait.
  destroy_scheduled_duration = "86400s"
}

# The signing backend signs with a version, whose key pair is fixed; the
# first version is the one the live test names.
resource "google_kms_crypto_key" "signing" {
  name     = "signing"
  key_ring = google_kms_key_ring.kek.id
  purpose  = "ASYMMETRIC_SIGN"

  version_template {
    algorithm        = "EC_SIGN_P256_SHA256"
    protection_level = "SOFTWARE"
  }

  destroy_scheduled_duration = "86400s"
}
