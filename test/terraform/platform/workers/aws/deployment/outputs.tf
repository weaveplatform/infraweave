# The coordinator validates this output against the inputs and the cluster
# stage, then writes it into workers.json.
output "hosting" {
  value = module.workers.hosting
}
