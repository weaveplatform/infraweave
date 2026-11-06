# The Google Cloud key the gcpkms KEK provider's live test wraps data keys
# with (decision 0013), and that its recorded fixtures were recorded
# against. Whoever runs the test needs Cloud KMS CryptoKey
# Encrypter/Decrypter on it; the project's owners have it already. No key
# material leaves Cloud KMS, and nothing here grants an outside identity.

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
