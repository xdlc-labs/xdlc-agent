import { useEffect, useId, useRef, useState } from "react";

/** Render a ```mermaid fence inside docs (lazy-loads mermaid). */
export function DocMermaid({ chart }: { chart: string }) {
  const id = useId().replace(/:/g, "");
  const ref = useRef<HTMLDivElement>(null);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    const run = async () => {
      try {
        const mermaid = (await import("mermaid")).default;
        mermaid.initialize({
          startOnLoad: false,
          securityLevel: "strict",
          theme: "dark",
          themeVariables: {
            darkMode: true,
            background: "#070a0c",
            primaryColor: "#0d3d38",
            primaryTextColor: "#e8f0ee",
            primaryBorderColor: "#2dd4bf",
            lineColor: "#5eead4",
            secondaryColor: "#12181c",
            tertiaryColor: "#0f1418",
            fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace",
            fontSize: "14px",
          },
        });
        const { svg } = await mermaid.render(`docs-mmd-${id}`, chart.trim());
        if (!cancelled && ref.current) {
          ref.current.innerHTML = svg;
          setErr(null);
        }
      } catch (e) {
        if (!cancelled) setErr(e instanceof Error ? e.message : "mermaid render failed");
      }
    };
    void run();
    return () => {
      cancelled = true;
    };
  }, [chart, id]);

  if (err) {
    return (
      <pre className="mt-4 overflow-x-auto rounded border border-breach/40 bg-breach/5 p-4 font-mono text-[12px] text-breach">
        {err}
        {"\n\n"}
        {chart}
      </pre>
    );
  }

  return (
    <div
      ref={ref}
      className="docs-mermaid mt-4 overflow-x-auto rounded border border-border bg-[#070a0c] p-4 [&_svg]:mx-auto [&_svg]:max-w-full"
      role="img"
      aria-label="Diagram"
    />
  );
}
