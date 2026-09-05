/** Console-local coding-agent credentials (browser only).
 *
 * Stored in localStorage — never baked into the SPA build. Sent to the
 * daemon only as Fix request headers (`X-XDLC-Agent-*`); the daemon
 * injects the key into the subprocess env for that run and must not
 * write it to audit / backlog / logs.
 */

const PROVIDER_KEY = "xdlc_agent_provider";
const API_KEY = "xdlc_agent_api_key";

export type AgentProvider = "claude" | "codex" | "cursor";

const PROVIDERS: AgentProvider[] = ["claude", "codex", "cursor"];

export function getAgentProvider(): AgentProvider | "" {
  if (typeof localStorage === "undefined") return "";
  const v = localStorage.getItem(PROVIDER_KEY) ?? "";
  return PROVIDERS.includes(v as AgentProvider) ? (v as AgentProvider) : "";
}

export function setAgentProvider(provider: AgentProvider | ""): void {
  if (!provider) {
    localStorage.removeItem(PROVIDER_KEY);
    return;
  }
  localStorage.setItem(PROVIDER_KEY, provider);
}

/** Raw key from localStorage; never log / put in URLs. */
export function getAgentAPIKey(): string {
  if (typeof localStorage === "undefined") return "";
  return localStorage.getItem(API_KEY) ?? "";
}

export function setAgentAPIKey(key: string): void {
  const trimmed = key.trim();
  if (!trimmed) {
    localStorage.removeItem(API_KEY);
    return;
  }
  localStorage.setItem(API_KEY, trimmed);
}

export function clearAgentCreds(): void {
  localStorage.removeItem(PROVIDER_KEY);
  localStorage.removeItem(API_KEY);
}

/** Headers for POST /api/actions/fix only. Empty when unset. */
export function agentFixHeaders(): HeadersInit {
  const out: Record<string, string> = {};
  const provider = getAgentProvider();
  const key = getAgentAPIKey();
  if (provider) out["X-XDLC-Agent-Provider"] = provider;
  if (key) out["X-XDLC-Agent-Key"] = key;
  return out;
}
