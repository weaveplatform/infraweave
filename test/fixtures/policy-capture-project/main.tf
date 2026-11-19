# This provider-free project gives the live Spacelift policy capture lab a
# non-empty plan without creating infrastructure or requiring credentials.
terraform {
  required_version = ">= 1.5.0, < 2.0.0"
}

output "capture" {
  value = "infraweave-policy-capture"
}
