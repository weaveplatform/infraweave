dependency "network" {
  config_path = "../network"

  mock_outputs = {
    network_id = "mock-network-id"
  }
  mock_outputs_allowed_terraform_commands = ["plan"]
}

inputs = {
  network_id = dependency.network.outputs.network_id
}
