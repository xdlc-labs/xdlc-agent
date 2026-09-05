variable "project_id" {
  description = "GCP project ID to create the cluster in (no default — always set explicitly)"
  type        = string
}

variable "region" {
  description = "GCP region for the GKE cluster (regional cluster, not zonal)"
  type        = string
  default     = "us-central1"
}

variable "cluster_name" {
  description = "GKE cluster name"
  type        = string
  default     = "xdlc"
}

variable "kubernetes_version" {
  description = "GKE release-channel-managed version prefix; leave empty to use the channel default"
  type        = string
  default     = ""
}

variable "node_machine_type" {
  description = "GCE machine type for the default node pool"
  type        = string
  default     = "e2-standard-2"
}

variable "node_count" {
  description = "Node count per zone in the default node pool (regional cluster spans 3 zones by default)"
  type        = number
  default     = 1
}
