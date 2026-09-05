# Disaster recovery and availability

What the agent stores on disk, how to back it up, and what happens when
that store is gone. Honest limits: there is **no built-in HA**.

## What lives on the PVC / data directory

Helm chart mounts a PVC (default `persistence.size: 2Gi`) as the
daemon working directory. Same contents for a binary run from a host
dir:

| Path | Purpose | Why exclusive (single writer) |
|---|---|---|
| `xdlc-agent-history.db` | bbolt audit DB (`xdlc-agent history`, `/api/history`, overview events) | Exclusive file lock |
| `BACKLOG.md` | Human + agent append-only action log | Multi-writer races |
| `repos/` | Clones of watched repos (`EnsureCloned` hard-resets from origin) | Working trees per Fix |

Config itself is not on the PVC (ConfigMap / `--config`); secrets live
in the Kubernetes Secret / env. Losing the PVC does **not** lose
GitHub/ArgoCD/Prometheus state — only local audit + clones + backlog
file.

`replicaCount` must stay **1**: bbolt takes an exclusive lock; RWO PVC
cannot attach to multiple writers. Rate limits and Fix concurrency are
also per-process — see [capacity.md](capacity.md).

## Backup

Pick one; anything that captures the volume consistently is fine.
There is no in-process backup API — these are Helm-templated, opt-in,
and off by default (both `false` in `values.yaml`; enabling both is
pointless, pick one).

### CSI VolumeSnapshot (`backup.csiSnapshot.enabled: true`)

Ships a namespaced ServiceAccount/Role/CronJob
(`templates/backup-csi-snapshot.yaml`) that creates a `VolumeSnapshot`
of the agent's PVC on `backup.csiSnapshot.schedule`, via a cluster
`VolumeSnapshotClass` you must already have
(`backup.csiSnapshot.volumeSnapshotClassName`). Does **not** scale the
Deployment to 0 first — same accepted torn-write risk as any live
snapshot; scale-to-0-first is a manual/advanced step, not automated,
so a failed job can't strand the Deployment at 0 replicas.

### Velero (`backup.velero.enabled: true`)

Ships a Velero `Schedule` (`templates/backup-velero-schedule.yaml`)
backing up the PVC in the agent namespace on
`backup.velero.schedule` — requires Velero already installed and
watching this cluster (or `backup.velero.veleroNamespace`). Include
the Secret in the schedule if you want a one-shot restore of
credentials — or recreate secrets from your vault separately (prefer
that; see [secrets.md](secrets.md)).

### `tar` of the data dir

```sh
# chart workingDir / PVC mount: /var/lib/xdlc-agent
kubectl -n xdlc exec deploy/xdlc-agent -- \
  tar -C /var/lib/xdlc-agent -cf - . \
  > xdlc-agent-data-$(date -u +%Y%m%dT%H%M%SZ).tar
```

Store the tarball off-cluster.

**Frequency:** set by your RPO tolerance (below). Daily is a common
starting point for audit continuity; the loop itself does not need the
DB to keep fixing/promoting once clones re-fetch.

## Restore

1. Scale the agent Deployment to 0 (or stop the binary).
2. Restore PVC contents from snapshot / Velero / untar into the empty
   volume (same layout: DB + `BACKLOG.md` + optional `repos/`).
3. Confirm file permissions match the chart non-root UID (`65532` in
   current values) if you restored as root.
4. Scale back to 1. Check logs + `GET /api/health`, then
   `GET /api/history` with a bearer token.
5. Optionally delete `repos/*` before start — daemon will re-clone on
   next Fix/promote/revert.

Do not restore an old DB onto a still-running writer.

## RPO / RTO (honest)

| Metric | Reality today |
|---|---|
| **RPO** | Time since last successful backup. No continuous replication. |
| **RTO** | Time to restore PVC + restart one pod (minutes if snapshot/Velero ready; longer if recreating volume by hand). |
| **HA** | None. `replicaCount: 1`. Pod death = loop pause until reschedule; no multi-writer failover. |
| **Cross-region** | Whatever your backup target provides — not a product feature. |

## High availability (not implemented)

The known open gap: leader election or a networked audit store. Stay on
one replica and back up the PVC. Do **not** set `replicaCount > 1` until
election **and** storage both land and are tested — raising replicas
today multiplies webhook/Fix budgets and corrupts local state.

One pod death = loop pause until Kubernetes reschedules that single
replica onto a node that can attach the RWO volume. No hot standby.

### Options (designed, not shipped)

**(A) Leader election (Lease) + shared PVC** — only the Lease holder
opens bbolt / appends `BACKLOG.md` / runs Fix/Promote/Revert. Still
blocked by RWO across nodes unless the volume moves with the leader or
you switch to RWX / a networked store.

**(B) Networked store for audit** — e.g. Postgres; clones can stay
ephemeral (`EnsureCloned` re-fetches). Reject SQLite-on-NFS. Rate-limit /
`max_concurrent_fixes` budgets must be lease-scoped or centralized.

**(C) Repo-sharding** — split `config.yaml` across Deployments (same as
[tenancy](capacity.md#tenancy-one-daemon-per-trust-domain)). Each shard
stays `replicaCount: 1` + its own PVC. This scales **coverage**, not HA
of a single shard.

| Choice today | Reason |
|---|---|
| Keep `replicaCount: 1` | Matches bbolt + RWO; avoids silent data races |
| Document SPOF + RPO/RTO | This page |
| Opt-in chart backup templates | Velero or CSI snapshot, both off by default |
| Election / networked store | Deferred until a hard HA requirement appears |

## Behavior on store loss or corruption

| Asset | If missing / corrupt |
|---|---|
| `xdlc-agent-history.db` | History / overview events / KPIs empty or fail to open. New dispatches can create a fresh DB. Past audit not reconstructible from GitHub alone. |
| `BACKLOG.md` | Regenerates from new events (header recreated on next append). Prior free-text notes lost. |
| `repos/` | Re-fetched from remotes on next action (`EnsureCloned`). Local unpushed WIP in the clone is gone — agent is not meant to hold unique commits only on disk. |

Corrupted bbolt: remove (or restore from backup) the DB file and
restart. Prefer restore if audit continuity matters.

Webhook delivery and gate polls resume after restart regardless of
audit state — you lose observability of *past* actions, not the ability
to take *new* ones (subject to GitHub/cluster credentials still being
valid).

## Related

- Deploy persistence: [deployment.md](deployment.md#persistence)
- Capacity / tenancy / knobs: [capacity.md](capacity.md)
- Security of what sits on disk: [SECURITY.md](../SECURITY.md)
