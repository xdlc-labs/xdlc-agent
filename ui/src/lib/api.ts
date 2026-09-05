// Client for xdlc-agent daemon /api/*. Fetch helpers throw on failure so
// React Query can distinguish loading / empty / error (issue #7). Soft
// "daemon stopped" shells are only built when a caller explicitly asks
// via emptyOverview().

import { authHeaders } from "./auth";

export type GateStatus = "pass" | "fail" | "acting" | "waiting" | "idle";
export type ActionKind = "Fix" | "Promote" | "Revert" | "Rerun" | "None";
export type GateName = "CI" | "DEV smoke" | "PROD health";
export type ManualAction = "fix" | "promote" | "revert";

export interface Repo {
  id: string;
  name: string;
  branch: string;
  lastGate: GateName;
  lastGateStatus: GateStatus;
  lastAction: ActionKind;
  lastActionAt: string;
  devTag: string;
  prodTag: string;
  health: "healthy" | "degraded" | "breach";
  cloneStatus: string;
  lastPromote: string;
  lastRevert: string;
  argocdApp: string;
  sloQueries: { label: string; query: string }[];
}

export interface Event {
  id: string;
  ts: string;
  repo: string;
  source: "github-actions" | "argocd" | "prometheus" | "daemon";
  gate: GateName | "daemon" | string;
  signal: string;
  action: ActionKind;
  ok: boolean;
  evidence: string;
  url?: string;
  chain_id?: string;
  seq?: number;
}

export interface Gate {
  name: GateName;
  provider: string;
  status: GateStatus;
  lastCheck: string;
  interval: string;
  trigger: string;
  evidence: string;
  url: string;
}

export interface Daemon {
  status: "running" | "degraded" | "stopped";
  version: string;
  env: string;
  uptime: string;
  webhook: string;
  configPath: string;
  gitopsDir: string;
  agentProvider: "claude" | "codex" | "cursor" | string;
}

export interface Overview {
  daemon: Daemon;
  pipeline: { stage: string; label: string; status: GateStatus; detail: string }[];
  kpis: {
    reposWatched: number;
    fixes: number;
    promotes: number;
    reverts: number;
    lastActionAt: string;
    backlogOpen: number;
  };
  gates: Gate[];
  repos: Repo[];
  events: Event[];
  backlogMd: string;
}

export const policy: {
  signal: string;
  source: string;
  action: ActionKind | "GitOps side-effect";
  note: string;
}[] = [
  { signal: "CI fail", source: "GitHub Actions", action: "Fix", note: "coding-agent subagent edits + pushes to the branch" },
  { signal: "CI pass", source: "GitHub Actions", action: "GitOps side-effect", note: "image tag write-back → ArgoCD syncs DEV. Not an agent action." },
  { signal: "DEV smoke fail", source: "k6 / Playwright", action: "Fix", note: "same subagent, smoke output supplied as context" },
  { signal: "DEV smoke pass", source: "k6 / Playwright", action: "Promote", note: "fast-forward develop→main; refused if non-FF" },
  { signal: "PROD p95 breach", source: "Prometheus", action: "Revert", note: "git revert on main, rollback-first" },
  { signal: "PROD error-rate breach", source: "Prometheus", action: "Revert", note: "git revert on main, rollback-first" },
];

/** Last /api fetch succeeded (module-level; updated by fetch helpers). */
export let backendReachable = true;

/** HTTP status of last failed fetch, or null if last fetch ok / network error. */
export let lastFetchStatus: number | null = null;

export function isDegraded(overview: Overview): boolean {
  return (
    overview.daemon.status === "stopped" ||
    overview.daemon.webhook.includes("backend unreachable") ||
    overview.daemon.webhook.includes("unauthorized") ||
    overview.daemon.webhook.includes("503")
  );
}

/** Build a stopped-daemon Overview shell (tests / explicit fallbacks only). */
export const emptyOverview = (webhook = "backend unreachable"): Overview => ({
  daemon: {
    status: "stopped",
    version: "—",
    env: "—",
    uptime: "—",
    webhook,
    configPath: "—",
    gitopsDir: "—",
    agentProvider: "claude",
  },
  pipeline: [
    { stage: "github", label: "GitHub", status: "idle", detail: "start xdlc-agent daemon" },
    { stage: "ci", label: "CI gate", status: "idle", detail: "—" },
    { stage: "dev", label: "DEV smoke", status: "idle", detail: "—" },
    { stage: "promote", label: "Promote", status: "idle", detail: "—" },
    { stage: "prod", label: "PROD health", status: "idle", detail: "—" },
  ],
  kpis: { reposWatched: 0, fixes: 0, promotes: 0, reverts: 0, lastActionAt: "—", backlogOpen: 0 },
  gates: [],
  repos: [],
  events: [],
  backlogMd: "# BACKLOG\n\n(daemon not reachable — run `xdlc-agent daemon`)\n",
});

export function degradeWebhook(status: number | null): string {
  if (status === 401) return "unauthorized (401) — set API token in Settings";
  if (status === 503) return "backend unreachable (503) — API token not configured on daemon";
  return "backend unreachable";
}

async function getJSON<T>(path: string): Promise<T> {
  const res = await fetch(path, { headers: { ...authHeaders() } });
  if (!res.ok) {
    backendReachable = false;
    lastFetchStatus = res.status;
    throw new Error(`${path} → ${res.status}`);
  }
  backendReachable = true;
  lastFetchStatus = null;
  return res.json() as Promise<T>;
}

export async function fetchOverview(): Promise<Overview> {
  return getJSON<Overview>("/api/overview");
}

export async function fetchHistory(limit = 200): Promise<Event[]> {
  const data = await getJSON<{ events: Event[] }>(`/api/history?limit=${limit}`);
  return data.events ?? [];
}

export async function fetchRepo(id: string): Promise<{ repo: Repo; timeline: Event[] }> {
  return getJSON<{ repo: Repo; timeline: Event[] }>(`/api/repos/${encodeURIComponent(id)}`);
}

/** Absolute SSE URL for /api/events (issue #6). */
export function eventsURL(): string {
  return "/api/events";
}

export async function fetchBacklog(): Promise<string> {
  const data = await getJSON<{ markdown: string }>("/api/backlog");
  return data.markdown ?? "";
}

export interface FixPR {
  repo: string;
  branch: string;
  number: number;
  url: string;
  state: string;
  at: string;
  merged?: boolean;
  title?: string;
  ci?: string;
  reviewer?: string;
  stale?: boolean;
}

/** Fix-PR work queue — only populated once fix_mode: pr is used. */
export async function fetchFixPRs(all = false): Promise<FixPR[]> {
  const q = all ? "?all=1" : "";
  const data = await getJSON<{ prs: FixPR[] }>(`/api/prs${q}`);
  return data.prs ?? [];
}

export interface CostKPIs {
  totals: {
    fixes: number;
    reverts: number;
    promotes: number;
    total_cost_usd: number;
    fix_success_rate: number | null;
    duration_p50_ms?: number;
    duration_p95_ms?: number;
  };
  repos: {
    repo: string;
    fixes: number;
    reverts: number;
    total_cost_usd: number;
    fix_success_rate: number | null;
  }[];
}

export async function fetchCostKPIs(): Promise<CostKPIs> {
  return getJSON<CostKPIs>("/api/kpis");
}

export async function postAction(
  action: ManualAction,
  repo: string,
): Promise<{ ok: boolean; message: string; status: number }> {
  const res = await fetch(`/api/actions/${action}`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      ...authHeaders(),
    },
    body: JSON.stringify({ repo, confirm: true }),
  });
  const text = await res.text();
  let message = text.trim() || res.statusText || String(res.status);
  try {
    const j = JSON.parse(text) as { message?: string; error?: string; ok?: boolean };
    message = j.message ?? j.error ?? message;
  } catch {
    /* plain text body */
  }
  if (!res.ok) {
    backendReachable = res.status !== 401 && res.status !== 503 ? backendReachable : false;
    lastFetchStatus = res.status;
    return { ok: false, message, status: res.status };
  }
  return { ok: true, message, status: res.status };
}
