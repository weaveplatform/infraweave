# The project_id the objectstore, kek and workers-identity roots take.
output "project_id" {
  value = google_project.lab.project_id
}

output "project_number" {
  value = google_project.lab.number
}
