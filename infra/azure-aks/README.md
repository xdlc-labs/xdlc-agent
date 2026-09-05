# Azure AKS starter (xdlc)

**This is a starter path, not a production multi-zone hardened reference.**
Same shape as `infra/aws-eks`: stand up a resource group + cluster, then
install ingress/ArgoCD/the xdlc chart yourself.

HCL syntax/formatting verified via `terraform fmt`; not `terraform
init`/`validate`'d against a live Azure subscription or with the provider
plugin downloaded (no network access in the environment that wrote this) —
review before first `apply`, same caveat as `infra/aws-eks`.

## Prerequisites

- [Azure CLI](https://learn.microsoft.com/en-us/cli/azure/install-azure-cli), authenticated (`az login`) with a subscription set
- [Terraform](https://developer.hashicorp.com/terraform/install) ≥ 1.5
- `kubectl` and `helm` on your PATH
- Subscription permissions to create resource groups, AKS clusters, and networking

## Apply

From repo root (or use `./scripts/bootstrap-cloud/azure.sh`):

```sh
cd infra/azure-aks
terraform init
terraform plan -out=tfplan
terraform apply tfplan
```

Optional overrides:

```sh
terraform apply \
  -var="location=westeurope" \
  -var="cluster_name=xdlc-lab" \
  -var="resource_group_name=xdlc-lab-rg"
```

Wire kubeconfig (command also in `terraform output configure_kubectl`):

```sh
az aks get-credentials --resource-group <resource_group_name> --name <cluster_name>
kubectl get nodes
```

## After the cluster exists

Terraform does **not** install ingress or ArgoCD. Same steps as
[`infra/aws-eks/README.md`](../aws-eks/README.md#after-the-cluster-exists)
(ingress-nginx, then Argo CD), then:

```sh
kubectl create namespace xdlc

kubectl create secret generic xdlc-agent-secrets \
  --namespace xdlc \
  --from-literal=GITHUB_WEBHOOK_SECRET=... \
  --from-literal=ANTHROPIC_API_KEY=...
  # plus GitHub App or PAT keys — see docs/deployment.md

helm upgrade --install xdlc-agent deploy/helm/xdlc-agent \
  --namespace xdlc \
  -f deploy/overlays/aks/values.yaml \
  --set-file config=config.yaml \
  --set existingSecret=xdlc-agent-secrets \
  --set image.tag=<release-tag>
```

Current AKS ships the Azure Disk CSI driver enabled by default — no
separate addon step like `infra/aws-eks`'s EBS CSI wiring. The overlay
points `persistence.storageClassName` at `managed-csi` (AKS's default CSI
StorageClass on current versions); check `kubectl get storageclass` if
your cluster's default differs.

## Destroy

```sh
cd infra/azure-aks
terraform destroy
```

Confirm no leftover LoadBalancers / disks from Helm releases before
destroy if apply fails on dependency order.

## What this is not

- Not multi-zone / availability-zone hardening
- Not least-privilege managed-identity wiring for every workload
- Not DNS / TLS / cert-manager automation
- Not a substitute for your org's landing-zone / policy story
