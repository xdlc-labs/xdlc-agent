import type { Event } from "@/lib/api";
import { ActionTag } from "@/components/status";

export function ActivityFeed({ items, dense = false }: { items: Event[]; dense?: boolean }) {
  if (items.length === 0) {
    return (
      <p className="px-4 py-10 text-center text-sm text-muted-foreground">
        No signals yet — the daemon writes here as soon as a gate reports.
      </p>
    );
  }

  return (
    <ol className="divide-y divide-border">
      {items.map((e, i) => (
        <li
          key={e.id}
          className="slide-in-event flex gap-4 px-4 py-3 hover:bg-surface"
          style={{ animationDelay: `${Math.min(i, 10) * 35}ms` }}
        >
          <div className="w-40 shrink-0 font-mono text-[11px] text-muted-foreground">{e.ts}</div>
          <div
            className={`w-0.5 shrink-0 ${
              e.action === "Revert"
                ? "bg-breach"
                : e.action === "Promote"
                  ? "bg-pass"
                  : e.action === "Fix"
                    ? "bg-acting"
                    : "bg-border"
            }`}
          />
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-mono text-[12px] text-foreground">{e.repo}</span>
              <span className="font-mono text-[11px] text-muted-foreground">
                {e.gate} · {e.signal}
              </span>
              <ActionTag action={e.action} />
              {!e.ok && (
                <span className="font-mono text-[11px] text-breach">fail</span>
              )}
            </div>
            {!dense && (
              <pre className="mt-1.5 whitespace-pre-wrap font-mono text-[11px] leading-relaxed text-muted-foreground">
                {e.evidence}
              </pre>
            )}
            {!dense && e.url && (
              <a
                href={e.url}
                className="mt-1 inline-block font-mono text-[11px] text-primary underline-offset-2 hover:underline"
              >
                {e.url}
              </a>
            )}
          </div>
        </li>
      ))}
    </ol>
  );
}
