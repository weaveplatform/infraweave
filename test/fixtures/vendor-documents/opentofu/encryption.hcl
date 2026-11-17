key_provider "pbkdf2" "dummy" {
  passphrase = "res11-dummy-state-passphrase"
}
method "aes_gcm" "dummy" {
  keys = key_provider.pbkdf2.dummy
}
state {
  method   = method.aes_gcm.dummy
  enforced = true
}
plan {
  method   = method.aes_gcm.dummy
  enforced = true
}
