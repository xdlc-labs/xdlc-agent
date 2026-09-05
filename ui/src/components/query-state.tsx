import type { ReactNode } from "react";

/** First-paint placeholder — distinct from empty and error. */
export function Skeleton({ rows = 3, className = "" }: { rows?: number; className?: string }) {
  return (
    <div className={`space-y-2 px-6 py-6 ${className}`} role="status" aria-label="Loading">
      {Array.from({ length: rows }, (_, i) => (
        <div
          key={i}
          className="h-3 animate-pulse rounded-sm bg-border/80"
          style={{ width: `${72 - i * 8}%` }}
        />
      ))}
    </div>
  );
}

/** Visible when a query fails — never reuse empty-config copy. */
export function QueryError({
  message,
  onRetry,
}: {
  message?: string;
  onRetry?: () => void;
}) {
  return (
    <div className="mx-6 my-6 border border-breach/40 bg-breach/5 px-4 py-3" role="alert">
      <p className="font-mono text-[12px] text-breach">
        {message?.trim() || "Backend request failed."}
      </p>
      {onRetry ? (
        <button
          type="button"
          onClick={onRetry}
          className="mt-2 font-mono text-[11px] uppercase tracking-[0.12em] text-foreground underline-offset-2 hover:underline"
        >
          retry
        </button>
      ) : null}
    </div>
  );
}

/** True empty state (data loaded, nothing there). */
export function EmptyState({ children }: { children: ReactNode }) {
  return <p className="px-6 py-6 font-mono text-[12px] text-muted-foreground">{children}</p>;
}
