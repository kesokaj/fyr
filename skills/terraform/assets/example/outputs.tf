output "service_uri" {
  description = "The Cloud Run service URL; the smoke check in DEPLOY-gcp hits <uri>/healthz"
  value       = google_cloud_run_v2_service.app.uri
}

output "runtime_service_account" {
  description = "The runtime SA email, for IAM bindings made outside this root"
  value       = google_service_account.run.email
}

output "image_repository" {
  description = "The Docker repository path cloud-run-deploy pushes to"
  value       = "${var.region}-docker.pkg.dev/${var.project}/${google_artifact_registry_repository.app.repository_id}"
}
