# Starter GKE stack — not a production multi-zone hardened reference.
# Plain resources (no community module), regional cluster, GCE Persistent
# Disk CSI is on by default in GKE (no separate addon step needed, unlike
# infra/aws-eks's EBS CSI wiring). See README.md.

terraform {
  required_version = ">= 1.5"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
}

resource "google_compute_network" "this" {
  name                    = "${var.cluster_name}-vpc"
  auto_create_subnetworks = false
}

resource "google_compute_subnetwork" "this" {
  name          = "${var.cluster_name}-subnet"
  network       = google_compute_network.this.id
  region        = var.region
  ip_cidr_range = "10.10.0.0/20"

  secondary_ip_range {
    range_name    = "pods"
    ip_cidr_range = "10.20.0.0/14"
  }
  secondary_ip_range {
    range_name    = "services"
    ip_cidr_range = "10.30.0.0/20"
  }
}

resource "google_container_cluster" "this" {
  name     = var.cluster_name
  location = var.region # regional cluster — spreads across 3 zones

  network    = google_compute_network.this.id
  subnetwork = google_compute_subnetwork.this.id

  ip_allocation_policy {
    cluster_secondary_range_name  = "pods"
    services_secondary_range_name = "services"
  }

  min_master_version = var.kubernetes_version != "" ? var.kubernetes_version : null

  # Managed node pool below owns the actual nodes; the cluster's own
  # default pool is removed to avoid two overlapping node pools.
  remove_default_node_pool = true
  initial_node_count       = 1

  deletion_protection = false # starter/lab default — set true before real use
}

resource "google_container_node_pool" "default" {
  name     = "${var.cluster_name}-default"
  cluster  = google_container_cluster.this.name
  location = var.region

  node_count = var.node_count # per zone; regional cluster = 3 zones

  node_config {
    machine_type = var.node_machine_type
    disk_size_gb = 50

    oauth_scopes = [
      "https://www.googleapis.com/auth/cloud-platform",
    ]
  }

  autoscaling {
    min_node_count = 1
    max_node_count = 4
  }
}
