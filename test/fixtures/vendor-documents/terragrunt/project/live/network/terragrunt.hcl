include "root" {
  path = find_in_parent_folders("root.hcl")
}

inputs = {
  cidr    = "10.0.0.0/16"
  api_key = "dummy-tg-api-key"
}
