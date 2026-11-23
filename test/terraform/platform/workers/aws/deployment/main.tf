# The deployment stage of the EKS worker hosting acceptance: the shared
# module from this checkout installs the worker chart and its pools into the
# cluster the cluster stage built or verified.
module "workers" {
  source = "../../../../../../deploy/terraform/modules/workers/aws"

  ownership                  = var.ownership
  cloud                      = var.cloud
  cluster_stage              = var.cluster_stage
  cluster                    = var.cluster
  namespace                  = var.namespace
  installation               = var.installation
  artifacts                  = var.artifacts
  module_revision            = var.module_revision
  server                     = var.server
  management_credentials_ref = var.management_credentials_ref
  pools                      = var.pools
  resources                  = var.resources
  timeouts                   = var.timeouts
}
