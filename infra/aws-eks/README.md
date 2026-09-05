# AWS EKS starter (xdlc)

**This is a starter path, not a production multi-AZ hardened reference.**

It stands up a VPC + EKS managed node group so you can install ingress,
ArgoCD, and the xdlc Helm chart. It deliberately skips IRSA-hardened
workloads, multi-NAT HA, private-only API, WAF, cluster encryption
config, and multi-region DR. Treat output as a lab / first-cloud
bootstrap, then harden for real traffic.

Same shape now exists for [GCP/GKE](../gcp-gke/README.md) and
[Azure/AKS](../azure-aks/README.md).

## Prerequisites

- [AWS CLI](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html) v2, credentials configured (`aws sts get-caller-identity` works)
- [Terraform](https://developer.hashicorp.com/terraform/install) ≥ 1.5
- `kubectl` and `helm` on your PATH
- IAM permissions to create VPC, EKS, EC2, IAM roles, and related resources

## Apply

From repo root (or use `./scripts/bootstrap-cloud/aws.sh`):

```sh
cd infra/aws-eks
terraform init
terraform plan -out=tfplan
terraform apply tfplan
```

Optional overrides:

```sh
terraform apply \
  -var="region=eu-west-1" \
  -var="cluster_name=xdlc-lab"
```

Wire kubeconfig (command also in `terraform output configure_kubectl`):

```sh
aws eks update-kubeconfig --region <region> --name <cluster_name>
kubectl get nodes
```

## After the cluster exists

Terraform does **not** install ingress or ArgoCD. Do that next, then
point the xdlc chart at the cluster.

### 1. Ingress (Contour)

ingress-nginx is archived (Mar 2026). Contour keeps the Ingress API.

```sh
helm repo add contour https://projectcontour.github.io/helm-charts/
helm upgrade --install contour contour/contour \
  --namespace projectcontour --create-namespace \
  --version 0.7.0 \
  --set envoy.service.type=LoadBalancer
```

Wait for an external hostname/IP on the Envoy Service, then point
DNS (or use the LB hostname directly for a lab). Service Ingresses in
this repo use `ingressClassName: contour`.

### 2. Argo CD

```sh
kubectl create namespace argocd
helm repo add argo https://argoproj.github.io/argo-helm
helm upgrade --install argocd argo/argo-cd \
  --namespace argocd \
  --version 10.7.0 \
  --set server.service.type=ClusterIP
```

Apply this repo's GitOps roots when ready (`gitops/root-dev.yaml`,
`gitops/root-prod.yaml`) — today they assume in-cluster destination;
multi-cluster wiring is not wired up yet (see `gitops/clusters/`).

### 3. xdlc agent (Helm)

```sh
kubectl create namespace xdlc

kubectl create secret generic xdlc-agent-secrets \
  --namespace xdlc \
  --from-literal=GITHUB_WEBHOOK_SECRET=... \
  --from-literal=ANTHROPIC_API_KEY=...
  # plus GitHub App or PAT keys — see docs/deployment.md

helm upgrade --install xdlc-agent deploy/helm/xdlc-agent \
  --namespace xdlc \
  -f deploy/overlays/eks/values.yaml \
  --set-file config=config.yaml \
  --set existingSecret=xdlc-agent-secrets \
  --set image.tag=<release-tag>
```

Overlay sets `require_webhook_secret: true`, a `gp3` PVC hint (EBS CSI
addon is enabled in this Terraform), and cloud-sized resources. If
`gp3` StorageClass is missing after the addon is Ready:

```sh
kubectl apply -f - <<'EOF'
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: gp3
provisioner: ebs.csi.aws.com
parameters:
  type: gp3
volumeBindingMode: WaitForFirstConsumer
EOF
```

Or set `persistence.storageClassName` to your cluster default (often `gp2`).
Full ops notes: [docs/deployment.md](../../docs/deployment.md).

## Node groups vs Fargate

This starter uses an **EKS managed node group**. Fargate profiles are a
fine alternative for some workloads but need extra IAM/subnet wiring and
are a worse fit for the agent's PVC (bbolt + repo clones). Stay on nodes
unless you redesign persistence.

## Destroy

```sh
cd infra/aws-eks
terraform destroy
```

Confirm the AWS console is clean of leftover LBs / volumes from Helm
releases before destroy if apply fails on dependency order.

## What this is not

- Not multi-AZ NAT / control-plane hardening
- Not least-privilege IRSA for every workload
- Not DNS / TLS / cert-manager automation
- Not a substitute for your org's landing-zone / SCP story
