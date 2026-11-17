unit "network" {
  source = "../catalog/units/network"
  path   = "network"

  values = {
    cidr = "10.1.0.0/16"
  }
}

unit "app" {
  source = "../catalog/units/app"
  path   = "app"
}
