// SessionPanel is the console view of one Fix recording: the meta.json
// summary, then the diff, the prompt or the output tail on demand.
//
// It reads /api/sessions/{id}, which is operator-only because a prompt
// embeds unscrubbed CI logs. A viewer token therefore lands on 403 here,
// and the panel says that plainly instead of "failed to load".
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchSession, fetchSessionText, type Session, type SessionFile } from "@/lib/api";
import { QueryError, Skeleton } from "@/components/query-state";

const tabs: { key: SessionFile; label: string }[] = [
  { key: "diff", label: "diff" },
  { key: "prompt", label: "prompt" },
  { key: "output", label: "output" },
];

export function SessionPanel({ id }: { id: string }) {
  const [tab, setTab] = useState<SessionFile | null>(null);
  const { data, isPending, isError, error, refetch } = useQuery({
    queryKey: ["session", id],
    queryFn: () => fetchSession(id),
    retry: false,
    // A recording is immutable once the Fix has ended, so never refetch
    // it. While it is still running, re-read on every mount (0) so the
    // panel picks up the ending.
    staleTime: (query) => (query.state.data?.session.ended_at ? Infinity : 0),
  });

  if (isPending) return <Skeleton rows={3} className="px-4 py-3" />;
  if (isError) {
    const msg = error instanceof Error ? error.message : "Failed to load session";
    if (msg.endsWith("403")) {
      return (
        <p className="px-4 py-3 font-mono text-[11px] text-muted-foreground">
          Recordings are operator-only: they hold the unscrubbed prompt and CI logs. Paste an operator
          token in Settings to read this one.
        </p>
      );
    }
    return <QueryError message={msg} onRetry={() => void refetch()} />;
  }

  const s = data.session;
  const attempt = s.attempts && s.attempts > 1 ? s.attempts : 1;
  return (
    <div className="border-t border-border bg-surface px-4 py-3 font-mono text-[11px]">
      <dl className="grid grid-cols-2 gap-x-6 gap-y-2 sm:grid-cols-3">
        {metaRows(s).map(([k, v]) => (
          <div key={k}>
            <dt className="text-[10px] uppercase tracking-wider text-muted-foreground">{k}</dt>
            <dd className="mt-0.5 break-all text-foreground">{v}</dd>
          </div>
        ))}
      </dl>
      {s.summary ? <p className="mt-3 text-foreground">{s.summary}</p> : null}
      {s.error ? <p className="mt-2 text-breach">{s.error}</p> : null}

      <div className="mt-3 flex flex-wrap items-center gap-2">
        <span className="text-[10px] uppercase tracking-wider text-muted-foreground">files</span>
        {tabs.map((t) => (
          <button
            key={t.key}
            type="button"
            aria-pressed={tab === t.key}
            onClick={() => setTab(tab === t.key ? null : t.key)}
            className={`border px-2 py-0.5 hover:bg-card ${
              tab === t.key ? "border-primary text-primary" : "border-border text-muted-foreground"
            }`}
          >
            {t.label}
          </button>
        ))}
        {data.files.length > 0 ? (
          <span className="ml-auto text-muted-foreground" title={data.files.join(", ")}>
            {data.files.length} on disk
          </span>
        ) : null}
      </div>

      {tab ? (
        <SessionText id={id} file={tab} attempt={attempt} ended={!!s.ended_at} />
      ) : data.output_tail ? (
        <div className="mt-3">
          <div className="mb-1 text-[10px] uppercase tracking-wider text-muted-foreground">output, last lines</div>
          <pre className="max-h-64 overflow-auto whitespace-pre-wrap leading-relaxed text-foreground">
            {data.output_tail}
          </pre>
        </div>
      ) : null}
    </div>
  );
}

function SessionText({
  id,
  file,
  attempt,
  ended,
}: {
  id: string;
  file: SessionFile;
  attempt: number;
  /** The parent's meta says the Fix has ended: its files no longer change. */
  ended: boolean;
}) {
  const { data, isPending, isError, error, refetch } = useQuery({
    queryKey: ["session", id, file, attempt],
    queryFn: () => fetchSessionText(id, file, attempt),
    retry: false,
    ...(ended ? { staleTime: Infinity } : {}),
  });
  if (isPending) return <Skeleton rows={4} className="mt-3" />;
  if (isError) {
    const msg = error instanceof Error ? error.message : "Failed to load";
    if (msg.endsWith("404")) {
      return (
        <p className="mt-3 text-muted-foreground">
          {file === "diff" ? "This run delivered no patch." : `No ${file} recorded for attempt ${attempt}.`}
        </p>
      );
    }
    return <QueryError message={msg} onRetry={() => void refetch()} />;
  }
  return (
    <pre
      className={`mt-3 max-h-[32rem] overflow-auto whitespace-pre-wrap leading-relaxed text-foreground ${
        file === "diff" ? "diff" : ""
      }`}
    >
      {file === "diff" ? <Diff patch={data} /> : data}
    </pre>
  );
}

// Diff colours added and removed lines. No highlighter: a unified diff's
// meaning is in the first byte of each line, and nothing else.
function Diff({ patch }: { patch: string }) {
  return (
    <>
      {patch.split("\n").map((line, i) => {
        let cls = "";
        if (line.startsWith("+++") || line.startsWith("---")) cls = "text-muted-foreground";
        else if (line.startsWith("@@")) cls = "text-primary";
        else if (line.startsWith("+")) cls = "text-pass";
        else if (line.startsWith("-")) cls = "text-breach";
        return (
          <span key={i} className={`block ${cls}`}>
            {line || " "}
          </span>
        );
      })}
    </>
  );
}

function metaRows(s: Session): [string, string][] {
  const rows: [string, string][] = [
    ["session", s.id],
    ["provider", s.provider],
    ["status", s.status || "—"],
    ["verdict", s.outcome || "—"],
    ["attempts", String(s.attempts ?? 1)],
    ["duration", s.duration_ms ? `${Math.round(s.duration_ms / 1000)}s` : "—"],
    ["cost", costString(s.cost)],
    ["changed", s.changed_files ? `${s.changed_files} file${s.changed_files === 1 ? "" : "s"}` : "—"],
    ["head", s.head_sha ? s.head_sha.slice(0, 12) : "—"],
  ];
  if (s.pr_url) rows.push(["pr", s.pr_url]);
  return rows;
}

function costString(cost?: Record<string, number>): string {
  if (!cost) return "—";
  const usd = cost["total_cost_usd"];
  if (typeof usd === "number") return `$${usd.toFixed(2)}`;
  const out = cost["output_tokens"];
  if (typeof out === "number") return `${out} out tokens`;
  return "—";
}
