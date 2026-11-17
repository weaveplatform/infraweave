# infraweave writes this file for the run. The run's credentials are in
# its environment, never here.
terraform {
  backend "http" {
    address        = "https://relay.internal:8443/state/v1/stacks/01JD6Z3Q4W8N5V2K7M9P0R1S3T/workspaces/a%2Fb"
    lock_address   = "https://relay.internal:8443/state/v1/stacks/01JD6Z3Q4W8N5V2K7M9P0R1S3T/workspaces/a%2Fb"
    unlock_address = "https://relay.internal:8443/state/v1/stacks/01JD6Z3Q4W8N5V2K7M9P0R1S3T/workspaces/a%2Fb"
    lock_method    = "LOCK"
    unlock_method  = "UNLOCK"
  }
}
