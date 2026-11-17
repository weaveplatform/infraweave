include "root" {
  path = find_in_parent_folders("root.hcl")
}

terraform {
  source = "../../modules/app"
}

dependency "network" {
  config_path = "../network"

  mock_outputs = {
    network_id = "mock-network-id"
    password   = "mock-password"
  }
  mock_outputs_allowed_terraform_commands = ["plan", "validate"]
}

inputs = {
  network_id = dependency.network.outputs.network_id
  password   = dependency.network.outputs.password
}
