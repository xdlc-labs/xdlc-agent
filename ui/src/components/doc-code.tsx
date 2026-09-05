import { useMemo, useState } from "react";
import hljs from "highlight.js/lib/core";
import bash from "highlight.js/lib/languages/bash";
import shell from "highlight.js/lib/languages/shell";
import yaml from "highlight.js/lib/languages/yaml";
import json from "highlight.js/lib/languages/json";
import go from "highlight.js/lib/languages/go";
import typescript from "highlight.js/lib/languages/typescript";
import javascript from "highlight.js/lib/languages/javascript";
import plaintext from "highlight.js/lib/languages/plaintext";

hljs.registerLanguage("bash", bash);
hljs.registerLanguage("sh", shell);
hljs.registerLanguage("shell", shell);
hljs.registerLanguage("zsh", bash);
hljs.registerLanguage("yaml", yaml);
hljs.registerLanguage("yml", yaml);
hljs.registerLanguage("json", json);
hljs.registerLanguage("go", go);
hljs.registerLanguage("typescript", typescript);
hljs.registerLanguage("ts", typescript);
hljs.registerLanguage("javascript", javascript);
hljs.registerLanguage("js", javascript);
hljs.registerLanguage("text", plaintext);
hljs.registerLanguage("plaintext", plaintext);

function langFromClass(className: string | undefined): string {
  const m = /language-([\w-]+)/.exec(className ?? "");
  return m?.[1] ?? "text";
}

/** Fenced code with language chrome, copy, and highlight.js — console dark theme. */
export function DocCodeBlock({ className, code }: { className?: string; code: string }) {
  const lang = langFromClass(className);
  const body = code.replace(/\n$/, "");
  const [copied, setCopied] = useState(false);

  const html = useMemo(() => {
    try {
      if (hljs.getLanguage(lang)) {
        return hljs.highlight(body, { language: lang, ignoreIllegals: true }).value;
      }
      return hljs.highlightAuto(body).value;
    } catch {
      return body.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
    }
  }, [body, lang]);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(body);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      /* ignore */
    }
  };

  return (
    <div className="docs-code group mt-4 overflow-hidden rounded border border-border bg-[#070a0c]">
      <div className="flex items-center justify-between border-b border-border/70 bg-surface/40 px-3 py-1.5">
        <span className="font-mono text-[10px] uppercase tracking-[0.14em] text-muted-foreground">{lang}</span>
        <button
          type="button"
          onClick={() => void copy()}
          className="font-mono text-[10px] uppercase tracking-wider text-muted-foreground hover:text-primary"
        >
          {copied ? "copied" : "copy"}
        </button>
      </div>
      <pre className="m-0 overflow-x-auto p-4 font-mono text-[12px] leading-relaxed text-foreground">
        <code className="hljs" dangerouslySetInnerHTML={{ __html: html }} />
      </pre>
    </div>
  );
}

/** ASCII / text diagrams — monospaced panel. */
export function DocAsciiDiagram({ code }: { code: string }) {
  return (
    <div className="docs-diagram mt-4 overflow-x-auto rounded border border-primary/25 bg-[#070a0c] p-4 shadow-[inset_0_0_0_1px_color-mix(in_oklab,var(--primary)_8%,transparent)]">
      <pre className="m-0 font-mono text-[12px] leading-[1.55] text-foreground/95 whitespace-pre">{code}</pre>
    </div>
  );
}
