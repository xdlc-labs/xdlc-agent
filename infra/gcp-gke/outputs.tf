output "cluster_name" {
  description = "GKE cluster name"
  value       = google_container_cluster.this.name
}

output "cluster_endpoint" {
  description = "GKE API server endpoint"
  value       = google_container_cluster.this.endpoint
  sensitive   = true
}

output "region" {
  description = "GCP region"
  value       = var.region
}

output "configure_kubectl" {
  description = "Command to write kubeconfig for this cluster"
  value       = "gcloud container clusters get-credentials ${google_container_cluster.this.name} --region ${var.region} --project ${var.project_id}"
}
