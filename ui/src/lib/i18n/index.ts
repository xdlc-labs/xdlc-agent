import en from "./en.json";

const catalog: Record<string, string> = en;

/** Look up English string by key. Missing keys return the key itself. */
export function t(key: string): string {
  return catalog[key] ?? key;
}
