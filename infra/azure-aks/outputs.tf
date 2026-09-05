output "cluster_name" {
  description = "AKS cluster name"
  value       = azurerm_kubernetes_cluster.this.name
}

output "resource_group_name" {
  description = "Resource group the cluster lives in"
  value       = azurerm_resource_group.this.name
}

output "cluster_endpoint" {
  description = "AKS API server host"
  value       = azurerm_kubernetes_cluster.this.kube_config.0.host
  sensitive   = true
}

output "configure_kubectl" {
  description = "Command to write kubeconfig for this cluster"
  value       = "az aks get-credentials --resource-group ${azurerm_resource_group.this.name} --name ${azurerm_kubernetes_cluster.this.name}"
}
