# The cluster stage of the EKS worker hosting acceptance: the shared module
# from this checkout, which builds an EKS Auto Mode cluster and its network
# or reads back an explicit existing one.
module "cluster" {
  source = "../../../../../../deploy/terraform/modules/workers/aws/cluster"

  ownership   = var.ownership
  cloud       = var.cloud
  cluster     = var.cluster
  network     = var.network
  stage_scope = var.stage_scope
}
