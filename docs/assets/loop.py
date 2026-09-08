#!/usr/bin/env python3
"""Regenerates the README hero: docs/assets/loop-light.svg and loop-dark.svg.

    python3 docs/assets/loop.py

Two files rather than one with a media query, because GitHub's own
`<picture>` + `prefers-color-scheme` is the only theme switch that is
reliable for a README image. Hand-authored coordinates: the point of the
picture is that the agent is reachable through exactly one gate, so the
layout has to make that literal rather than decorative.

The viewBox is 900 wide and the README renders it at 900, so every font
size here is the size a reader actually gets.
"""

import pathlib

W, H = 900, 452

LIGHT = dict(
    ink="#1f2328", mid="#57606a", faint="#8c959f",
    line="#d0d7de", panel="#f6f8fa", panel2="#ffffff",
    red="#cf222e", green="#1a7f37", blue="#0969da", host="#8250df",
)
DARK = dict(
    ink="#e6edf3", mid="#9198a1", faint="#6e7681",
    line="#3d444d", panel="#151b23", panel2="#0d1117",
    red="#f85149", green="#3fb950", blue="#4493f8", host="#ab7df8",
)

# Rows: each signal, its action, and its outcome share one baseline.
FIX_Y, PROMOTE_Y, REVERT_Y = 126, 214, 270

SIG_X, SIG_W = 8, 122
POL_X, POL_W = 182, 118
POL_Y, POL_H = 88, 214
FIX_X, FIX_W = 340, 330
FIX_BOX_Y, FIX_BOX_H = 84, 108
OUT_X, OUT_W = 724, 168
BOX_X, BOX_Y, BOX_W, BOX_H = 166, 52, 520, 350


def esc(s):
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def text(x, y, s, size=11, fill="ink", weight=None, anchor="start", mono=False):
    fam = ("ui-monospace,SFMono-Regular,Menlo,Consolas,monospace" if mono
           else "-apple-system,BlinkMacSystemFont,Segoe UI,Helvetica,Arial,sans-serif")
    w = f' font-weight="{weight}"' if weight else ""
    a = f' text-anchor="{anchor}"' if anchor != "start" else ""
    return (f'<text x="{x}" y="{y}" font-family="{fam}" font-size="{size}"'
            f' fill="var(--{fill})"{w}{a}>{esc(s)}</text>')


def box(x, y, w, h, fill="panel", stroke="line", rx=8, dash=None, sw=1):
    d = f' stroke-dasharray="{dash}"' if dash else ""
    return (f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{rx}"'
            f' fill="var(--{fill})" stroke="var(--{stroke})" stroke-width="{sw}"{d}/>')


def arrow(x1, y, x2, color="mid", dash=None):
    d = f' stroke-dasharray="{dash}"' if dash else ""
    return (f'<line x1="{x1}" y1="{y}" x2="{x2 - 7}" y2="{y}" stroke="var(--{color})"'
            f' stroke-width="1.5"{d} marker-end="url(#a-{color})"/>')


def varrow(x, y1, y2, color="mid", dash=None):
    d = f' stroke-dasharray="{dash}"' if dash else ""
    return (f'<line x1="{x}" y1="{y1}" x2="{x}" y2="{y2 - 7}" stroke="var(--{color})"'
            f' stroke-width="1.5"{d} marker-end="url(#a-{color})"/>')


def dot(x, y, color):
    return f'<circle cx="{x}" cy="{y}" r="4" fill="var(--{color})"/>'


def signal(y, title, sub, color):
    """A signal chip: what the outside world reported."""
    top = y - 24
    return "".join([
        box(SIG_X, top, SIG_W, 48, fill="panel2"),
        dot(SIG_X + 14, y - 6, color),
        text(SIG_X + 26, y - 2, title, 12, "ink", "600"),
        text(SIG_X + 12, y + 15, sub, 9, "mid", mono=True),
    ])


def outcome(y, h, title, sub, color, strong=False):
    top = y - h // 2
    return "".join([
        box(OUT_X, top, OUT_W, h, fill="panel2", stroke=color if strong else "line",
            sw=1.5 if strong else 1),
        text(OUT_X + 14, top + 21, title, 12, "ink", "600"),
        text(OUT_X + 14, top + 37, sub, 9, "mid", mono=True),
    ] + ([text(OUT_X + 14, top + 51, "green checks, cost recorded", 9, "green", mono=True)]
         if strong else []))


def svg(pal):
    markers = "".join(
        f'<marker id="a-{c}" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6"'
        f' markerHeight="6" orient="auto-start-reverse">'
        f'<path d="M0,1 L9,5 L0,9 z" fill="var(--{c})"/></marker>'
        for c in ("mid", "faint", "green", "red")
    )
    p = []

    # The boundary that matters: what runs on hardware you own.
    p.append(box(BOX_X, BOX_Y, BOX_W, BOX_H, fill="panel2", stroke="host", rx=10, dash="5 4"))
    p.append(text(BOX_X + 14, BOX_Y + 20, "your host  ·  your keys  ·  nothing phones home",
                  10, "host", "600", mono=True))

    # Signals in, from GitHub and Prometheus.
    p.append(signal(FIX_Y, "CI red", "workflow_run: failure", "red"))
    p.append(signal(PROMOTE_Y, "DEV green", "smoke probe passed", "green"))
    p.append(signal(REVERT_Y, "prod breach", "p95 over threshold", "red"))

    # Policy: the whole point of the picture.
    p.append(box(POL_X, POL_Y, POL_W, POL_H))
    p.append(text(POL_X + 14, POL_Y + 26, "policy", 15, "ink", "700"))
    p.append(text(POL_X + 14, POL_Y + 44, "decides before", 10, "mid"))
    p.append(text(POL_X + 14, POL_Y + 58, "anything runs", 10, "mid"))
    p.append(f'<line x1="{POL_X + 14}" y1="{POL_Y + 72}" x2="{POL_X + POL_W - 14}"'
             f' y2="{POL_Y + 72}" stroke="var(--line)" stroke-width="1"/>')
    for i, s in enumerate(("flap detection", "circuit breaker", "depends_on", "budgets, caps")):
        p.append(text(POL_X + 14, POL_Y + 92 + i * 15, s, 9, "mid", mono=True))

    # What a Fix actually is. One box, because it is one gated path.
    p.append(box(FIX_X, FIX_BOX_Y, FIX_W, FIX_BOX_H, stroke="blue", sw=1.5))
    p.append(text(FIX_X + 16, FIX_BOX_Y + 24, "Fix", 15, "ink", "700"))
    p.append(text(FIX_X + 52, FIX_BOX_Y + 24, "the only path to the agent", 10, "blue", mono=True))
    p.append(text(FIX_X + 16, FIX_BOX_Y + 46, "1. git worktree per Fix, on a scratch branch", 10, "mid", mono=True))
    p.append(text(FIX_X + 16, FIX_BOX_Y + 63, "2. your agent CLI runs in it, with the failing logs", 10, "mid", mono=True))
    p.append(text(FIX_X + 16, FIX_BOX_Y + 80, "3. the agent commits, xdlc pushes", 10, "mid", mono=True))
    p.append(text(FIX_X + 16, FIX_BOX_Y + 97, "claude · codex · cursor · gemini", 9, "faint", mono=True))

    # Signal edges into policy.
    for y in (FIX_Y, PROMOTE_Y, REVERT_Y):
        p.append(arrow(SIG_X + SIG_W, y, POL_X))

    # Policy's four verdicts.
    p.append(arrow(POL_X + POL_W, FIX_Y, FIX_X))
    p.append(text((POL_X + POL_W + FIX_X) // 2, FIX_Y - 8, "fix", 10, "ink", "700", "middle", mono=True))

    p.append(arrow(POL_X + POL_W, PROMOTE_Y, OUT_X))
    p.append(text(POL_X + POL_W + 12, PROMOTE_Y - 8, "promote", 10, "ink", "700", mono=True))

    p.append(arrow(POL_X + POL_W, REVERT_Y, OUT_X))
    p.append(text(POL_X + POL_W + 12, REVERT_Y - 8, "revert", 10, "ink", "700", mono=True))

    p.append(varrow(POL_X + POL_W // 2, POL_Y + POL_H, 330, color="faint", dash="4 4"))
    p.append(text(POL_X + 6, 346, "noop — where most signals end", 10, "faint", "700", mono=True))

    # Fix's result leaves the host and lands back on GitHub.
    p.append(arrow(FIX_X + FIX_W, FIX_Y, OUT_X, color="green"))

    # Outcomes out.
    p.append(outcome(FIX_Y, 68, "pull request", "agent's summary, run link", "green", strong=True))
    p.append(outcome(PROMOTE_Y, 48, "develop → main", "fast-forward, gated SHA", "line"))
    p.append(outcome(REVERT_Y, 48, "main reverted", "before anyone pages you", "line"))

    # The audit trail, which is what makes the rest reviewable.
    band_y = 356
    p.append(box(POL_X, band_y, FIX_X + FIX_W - POL_X, 34, fill="panel", rx=6))
    p.append(text(POL_X + 16, band_y + 22,
                  "every action recorded: prompt · agent output · diff · verdict · cost",
                  10, "mid", mono=True))

    # Close the cycle. A merged Fix, a promote and a revert all change the
    # state the next signal is measured against -- that is the loop the
    # project is named for, so the picture has to actually loop.
    p.append(f'<path d="M{OUT_X + OUT_W // 2},{FIX_Y - 34} V32 Q{OUT_X + OUT_W // 2},22'
             f' {OUT_X + OUT_W // 2 - 10},22 H{SIG_X + SIG_W // 2 + 10} Q{SIG_X + SIG_W // 2},22'
             f' {SIG_X + SIG_W // 2},32 V{FIX_Y - 31}" fill="none" stroke="var(--faint)"'
             f' stroke-width="1.5" stroke-dasharray="4 4" marker-end="url(#a-faint)"/>')
    p.append(text(W // 2, 16, "the outcome is what the next signal measures", 10, "faint",
                  anchor="middle", mono=True))

    out = (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}" width="{W}"'
           f' height="{H}" role="img" aria-label="xdlc: signals from CI, DEV smoke and prod'
           f' health enter a policy gate that decides Fix, Promote, Revert or noop; only Fix'
           f' reaches the coding agent, which runs in a per-Fix git worktree on your own host">'
           f'<defs>{markers}</defs>{"".join(p)}</svg>')
    for key, value in pal.items():
        out = out.replace(f"var(--{key})", value)
    assert "var(--" not in out, "unmapped color"
    return out


here = pathlib.Path(__file__).parent
for name, pal in (("loop-light.svg", LIGHT), ("loop-dark.svg", DARK)):
    (here / name).write_text(svg(pal))
    print(here / name)
