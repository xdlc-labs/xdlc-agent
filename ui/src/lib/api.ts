// Client for the xdlc daemon /api/*. Fetch helpers throw on failure so
// React Query can distinguish loading / empty / error (issue #7). Soft
// "daemon stopped" shells are only built when a caller explicitly asks
// via emptyOverview().
//
// Types mirror handlers; contract source of truth: openapi/openapi.yaml (#15).

import { authHeaders } from "./auth";
import { agentFixHeaders } from "./agent-creds";

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
  /** Key into /api/sessions/{id} for a recorded Fix; empty otherwise. */
  session_id?: string;
  /** Why ok is false: the dispatch error verbatim. */
  error?: string;
}

/** meta.json of one Fix recording (internal/session.Meta). */
export interface Session {
  id: string;
  repo: string;
  source: string;
  kind: string;
  provider: string;
  fix_mode?: string;
  manual?: boolean;
  started_at: string;
  ended_at?: string;
  duration_ms?: number;
  status?: "ok" | "error" | "";
  error?: string;
  base_sha?: string;
  head_sha?: string;
  branch?: string;
  changed_files?: number;
  pr_url?: string;
  cost?: Record<string, number>;
  attempts?: number;
  outcome?: "fixed" | "gave_up" | "needs_human" | "";
  summary?: string;
}

export interface SessionDetail {
  session: Session;
  files: string[];
  output_tail: string;
}

export type SessionFile = "diff" | "prompt" | "output";

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
    { stage: "github", label: "GitHub", status: "idle", detail: "start xdlc daemon" },
    { stage: "ci", label: "CI gate", status: "idle", detail: "—" },
    { stage: "dev", label: "DEV smoke", status: "idle", detail: "—" },
    { stage: "promote", label: "Promote", status: "idle", detail: "—" },
    { stage: "prod", label: "PROD health", status: "idle", detail: "—" },
  ],
  kpis: { reposWatched: 0, fixes: 0, promotes: 0, reverts: 0, lastActionAt: "—", backlogOpen: 0 },
  gates: [],
  repos: [],
  events: [],
  backlogMd: "# BACKLOG\n\n(daemon not reachable — run `xdlc daemon`)\n",
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

/** Operator only: the recordings are unscrubbed, so a viewer token gets 403. */
export async function fetchSessions(repo?: string, limit = 50): Promise<{ enabled: boolean; sessions: Session[] }> {
  const q = new URLSearchParams({ limit: String(limit) });
  if (repo) q.set("repo", repo);
  const data = await getJSON<{ enabled: boolean; sessions: Session[] }>(`/api/sessions?${q}`);
  return { enabled: data.enabled, sessions: data.sessions ?? [] };
}

export async function fetchSession(id: string): Promise<SessionDetail> {
  return getJSON<SessionDetail>(`/api/sessions/${encodeURIComponent(id)}`);
}

/**
 * One text file of a recording. 404 means the run has no such file — a Fix
 * that changed nothing has no diff — and is reported as an Error whose
 * message ends in "404" so the panel can say so instead of "failed".
 */
export async function fetchSessionText(id: string, file: SessionFile, attempt = 1): Promise<string> {
  const q = file === "diff" ? "" : `?attempt=${attempt}`;
  const res = await fetch(`/api/sessions/${encodeURIComponent(id)}/${file}${q}`, {
    headers: { ...authHeaders() },
  });
  if (!res.ok) {
    throw new Error(`/api/sessions/${id}/${file} → ${res.status}`);
  }
  return res.text();
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

/**
 * Phase an in-flight Fix is in. A run skips what it does not do: no
 * "planning" without agent.fix_plan, no "pushing" outside worktree
 * mode, no "verifying" without agent.fix_reverify. "ok" and "error" are
 * terminal and only ever arrive as an event — a finished Fix is not in
 * /api/fixes/active.
 */
export type FixState =
  | "queued"
  | "cloning"
  | "planning"
  | "fixing"
  | "pushing"
  | "verifying"
  | "ok"
  | "error";

export interface ActiveFix {
  /** Stable for the whole run, including the queued phase. */
  id: string;
  /** The recording this Fix is writing; absent while queued. */
  session_id?: string;
  repo: string;
  source: string;
  provider?: string;
  state: FixState;
  /** When the Fix entered this state, not when it started. */
  since: string;
  attempt?: number;
}

/** Fixes running right now. Empty is the normal answer, not an error. */
/** One "fix_output" SSE event: a chunk of a running Fix's agent output. */
export interface FixOutput {
  id: string;
  repo?: string;
  text: string;
  /** The whole tail so far; replaces what the client holds. */
  snapshot?: boolean;
}

export async function fetchActiveFixes(): Promise<ActiveFix[]> {
  const data = await getJSON<{ fixes: ActiveFix[] }>("/api/fixes/active");
  return data.fixes ?? [];
}

export interface CostKPIs {
  totals: {
    repo?: string;
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
    promotes?: number;
    total_cost_usd: number;
    fix_success_rate: number | null;
    duration_p50_ms?: number;
    duration_p95_ms?: number;
  }[];
}

export async function fetchCostKPIs(): Promise<CostKPIs> {
  return getJSON<CostKPIs>("/api/kpis");
}

export async function postAction(
  action: ManualAction,
  repo: string,
  /** Operator note for a manual Fix — trusted text, ignored elsewhere. */
  instructions?: string,
): Promise<{ ok: boolean; message: string; status: number }> {
  const note = action === "fix" ? instructions?.trim() : "";
  const res = await fetch(`/api/actions/${action}`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      ...authHeaders(),
      ...(action === "fix" ? agentFixHeaders() : {}),
    },
    body: JSON.stringify({ repo, confirm: true, ...(note ? { instructions: note } : {}) }),
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
