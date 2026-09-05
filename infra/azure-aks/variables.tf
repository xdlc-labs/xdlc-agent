variable "location" {
  description = "Azure region for the AKS cluster"
  type        = string
  default     = "eastus"
}

variable "resource_group_name" {
  description = "Resource group name (created by this stack)"
  type        = string
  default     = "xdlc-rg"
}

variable "cluster_name" {
  description = "AKS cluster name"
  type        = string
  default     = "xdlc"
}

variable "kubernetes_version" {
  description = "AKS Kubernetes version; leave empty to use AKS's current default"
  type        = string
  default     = ""
}

variable "node_vm_size" {
  description = "VM size for the default node pool"
  type        = string
  default     = "Standard_D2s_v5"
}

variable "node_count" {
  description = "Node count in the default node pool"
  type        = number
  default     = 2
}
