# Multi-cluster GitOps

Two topologies. Pick one.

## Local (single cluster) — default today

Kind / Minikube: one kube-apiserver, `dev` and `prod` as **namespaces**.
Bootstrap still applies `gitops/root-dev.yaml` + `gitops/root-prod.yaml`
(see `scripts/bootstrap-local.sh --with-gitops`). Those roots discover
`gitops/apps/{dev,prod}/*` and deploy to `https://kubernetes.default.svc`.

No ApplicationSet required.

## Multi-cluster — ApplicationSet path

When ArgoCD manages **more than one registered cluster** (or you want one
generator for many roots), apply:

```bash
kubectl apply -f gitops/clusters/applicationset-root.yaml
```

That ApplicationSet uses a **list generator** (no live cluster-secret API).
Each entry is a logical cluster name → destination server URL → apps path.

Out of the box it emits the same two roots Kind already uses, both pointed
at the in-cluster API:

| element     | server                          | path             |
|-------------|---------------------------------|------------------|
| `local-dev` | `https://kubernetes.default.svc` | `gitops/apps/dev`  |
| `local-prod`| `https://kubernetes.default.svc` | `gitops/apps/prod` |

**Do not** apply both the ApplicationSet and `root-*.yaml` for the same
logical roots — you will get duplicate Applications. Kind stays on
`root-*.yaml`; real multi-cluster switches to the ApplicationSet.

### Adding a real remote cluster (e.g. `us-east-1`)

1. Register the cluster with ArgoCD (`argocd cluster add …` or equivalent
   secret in the `argocd` namespace).
2. Add a list element in `applicationset-root.yaml`:

   ```yaml
   - cluster: us-east-1
     server: https://XXXX.gr7.us-east-1.eks.amazonaws.com
     path: gitops/apps/prod   # or a dedicated path later
   ```

3. Commit + sync. ArgoCD creates `root-us-east-1` targeting that server.

Optional later: per-cluster app trees under `gitops/clusters/<name>/`
(see `local-dev/`). Not required while apps still live in `gitops/apps/`.

## Why list generator, not cluster generator

Cluster generator reads ArgoCD's cluster secrets at reconcile time — fine in
a live control plane, awkward for a checked-in example that must render
without those secrets. List generator is explicit, reviewable in git, and
enough until cluster inventory itself should drive the set.
