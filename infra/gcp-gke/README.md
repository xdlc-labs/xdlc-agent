# GCP GKE starter (xdlc)

**This is a starter path, not a production multi-zone hardened reference.**
Same shape as `infra/aws-eks`: stand up a network + cluster, then install
ingress/ArgoCD/the xdlc chart yourself.

HCL syntax/formatting verified via `terraform fmt`; not `terraform
init`/`validate`'d against a live GCP project or with the provider plugin
downloaded (no network access in the environment that wrote this) — review
before first `apply`, same caveat as `infra/aws-eks`.

## Prerequisites

- [`gcloud` CLI](https://cloud.google.com/sdk/docs/install), authenticated (`gcloud auth login`) with a project set
- [Terraform](https://developer.hashicorp.com/terraform/install) ≥ 1.5
- `kubectl` and `helm` on your PATH
- A GCP project with the Kubernetes Engine API enabled and IAM permissions to create networks, GKE clusters, and node pools

## Apply

From repo root (or use `./scripts/bootstrap-cloud/gcp.sh`):

```sh
cd infra/gcp-gke
terraform init
terraform plan -out=tfplan -var="project_id=<your-project-id>"
terraform apply tfplan
```

Optional overrides:

```sh
terraform apply \
  -var="project_id=<your-project-id>" \
  -var="region=europe-west1" \
  -var="cluster_name=xdlc-lab"
```

Wire kubeconfig (command also in `terraform output configure_kubectl`):

```sh
gcloud container clusters get-credentials <cluster_name> --region <region> --project <project_id>
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
  -f deploy/overlays/gke/values.yaml \
  --set-file config=config.yaml \
  --set existingSecret=xdlc-agent-secrets \
  --set image.tag=<release-tag>
```

GKE ships the GCE Persistent Disk CSI driver enabled by default — no
separate addon step like `infra/aws-eks`'s EBS CSI wiring. The overlay
points `persistence.storageClassName` at `standard-rwo` (GKE's default
CSI StorageClass on current versions); check `kubectl get storageclass`
if your cluster's default differs.

## Destroy

```sh
cd infra/gcp-gke
terraform destroy -var="project_id=<your-project-id>"
```

Confirm no leftover LoadBalancers / disks from Helm releases before
destroy if apply fails on dependency order.

## What this is not

- Not multi-zone control-plane hardening beyond GKE's own regional-cluster default
- Not Workload Identity wiring for every workload
- Not DNS / TLS / cert-manager automation
- Not a substitute for your org's landing-zone / org-policy story
