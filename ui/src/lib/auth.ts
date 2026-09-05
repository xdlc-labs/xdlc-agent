const STORAGE_KEY = "xdlc_api_token";

/** Read bearer token: localStorage first, then optional VITE_API_TOKEN. */
export function getToken(): string {
  if (typeof localStorage !== "undefined") {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored) return stored;
  }
  const env = import.meta.env["VITE_API_TOKEN"];
  return typeof env === "string" && env.length > 0 ? env : "";
}

export function setToken(token: string): void {
  localStorage.setItem(STORAGE_KEY, token);
}

export function clearToken(): void {
  localStorage.removeItem(STORAGE_KEY);
}

/** Authorization header when a token is set; otherwise empty. */
export function authHeaders(): HeadersInit {
  const token = getToken();
  return token ? { Authorization: `Bearer ${token}` } : {};
}

export type Role = "operator" | "viewer" | null;

export type AuthConfig = {
  enabled: boolean;
  loginUrl?: string;
  logoutUrl?: string;
};

/**
 * GET /auth/config — public, unauthenticated. Reports whether the daemon
 * has OIDC SSO configured at all, so the console doesn't have to guess
 * or hardcode /auth/login. { enabled: false } on any fetch failure
 * (daemon down, route not mounted because OIDC isn't configured).
 */
export async function fetchAuthConfig(): Promise<AuthConfig> {
  try {
    const res = await fetch("/auth/config");
    if (!res.ok) return { enabled: false };
    return (await res.json()) as AuthConfig;
  } catch {
    return { enabled: false };
  }
}

/**
 * GET /api/whoami — bearer token or OIDC session cookie, whichever is
 * set. null role means "not authenticated by either method", not an
 * error — the console should show a login prompt, not a broken state.
 */
export async function fetchRole(): Promise<Role> {
  try {
    const res = await fetch("/api/whoami", { headers: { ...authHeaders() } });
    if (!res.ok) return null;
    const data = (await res.json()) as { role?: string };
    return data.role === "operator" || data.role === "viewer" ? data.role : null;
  } catch {
    return null;
  }
}
