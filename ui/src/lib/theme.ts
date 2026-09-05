export type Theme = "light" | "dark";

const STORAGE_KEY = "xdlc_theme";

export function resolveTheme(): Theme {
  if (typeof window === "undefined") return "dark";
  const stored = localStorage.getItem(STORAGE_KEY);
  if (stored === "light" || stored === "dark") return stored;
  if (window.matchMedia("(prefers-color-scheme: light)").matches) return "light";
  return "dark";
}

export function applyTheme(theme: Theme) {
  document.documentElement.setAttribute("data-theme", theme);
  document.documentElement.style.colorScheme = theme;
}

export function setTheme(theme: Theme) {
  localStorage.setItem(STORAGE_KEY, theme);
  applyTheme(theme);
}

export function toggleTheme(): Theme {
  const next: Theme = resolveTheme() === "dark" ? "light" : "dark";
  setTheme(next);
  return next;
}

// The pre-paint FOUC-avoidance boot script (same storage key + fallback
// logic as resolveTheme() above) lives inline in index.html, since it
// must run before the React bundle loads. Keep the two in sync by hand
// if STORAGE_KEY or the fallback rule changes here.
