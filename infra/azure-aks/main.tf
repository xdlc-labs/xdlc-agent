# Starter AKS stack — not a production multi-zone hardened reference.
# Plain resources (no community module). Azure Disk CSI ("managed-csi"
# StorageClass) ships enabled by default on current AKS versions — no
# separate addon step needed, unlike infra/aws-eks's EBS CSI wiring.
# See README.md.

terraform {
  required_version = ">= 1.5"

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 3.100"
    }
  }
}

provider "azurerm" {
  features {}
}

resource "azurerm_resource_group" "this" {
  name     = var.resource_group_name
  location = var.location
}

resource "azurerm_kubernetes_cluster" "this" {
  name                = var.cluster_name
  location            = azurerm_resource_group.this.location
  resource_group_name = azurerm_resource_group.this.name
  dns_prefix          = var.cluster_name
  kubernetes_version  = var.kubernetes_version != "" ? var.kubernetes_version : null

  default_node_pool {
    name       = "default"
    vm_size    = var.node_vm_size
    node_count = var.node_count

    upgrade_settings {
      max_surge = "10%"
    }
  }

  identity {
    type = "SystemAssigned"
  }

  network_profile {
    network_plugin = "azure"
    network_policy = "azure"
  }

  tags = {
    project = "xdlc"
  }
}
