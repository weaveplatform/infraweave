# The coordinator validates this output, completes a verified TLS handshake
# with its endpoint, and hands it to the deployment stage as cluster_stage.
output "cluster" {
  value = module.cluster.cluster
}
