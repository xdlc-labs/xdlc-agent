import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

/** Relative age from audit `at` ("2006-01-02 15:04:05Z") or ISO. */
export function formatAge(at: string, now = Date.now()): string {
  const ms = Date.parse(at.includes("T") ? at : at.replace(" ", "T"));
  if (Number.isNaN(ms)) return at;
  const sec = Math.max(0, Math.floor((now - ms) / 1000));
  if (sec < 60) return `${sec}s`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m`;
  const hr = Math.floor(min / 60);
  if (hr < 48) return `${hr}h`;
  return `${Math.floor(hr / 24)}d`;
}
