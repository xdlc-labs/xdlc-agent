#!/usr/bin/env python3
"""Regenerates the README hero: docs/assets/loop-light.svg and loop-dark.svg.

    python3 docs/assets/loop.py

Two files rather than one with a media query, because GitHub's `<picture>`
plus `prefers-color-scheme` is the only theme switch that is reliable for a
README image. Colors are substituted in below rather than carried as CSS
custom properties: GitHub's SVG sanitizer is free to drop a style
attribute, and a hero that renders black-on-black is worse than no hero.

Hand-authored coordinates. The claim the picture has to make is that the
agent sits behind exactly one of four verdicts, so the layout makes that
literal: one row per signal, and only the top row reaches the agent box.

Sizing: GitHub renders a README image at the width of its content column,
roughly 660px on a laptop rather than the 900 of this viewBox. Every font
here is therefore about 0.73x on screen, which is why nothing is smaller
than 10 and why the strings are as short as they are. Lengthen one and it
will overflow its box on a real page -- check a render, because the SVG
itself will not complain.
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

# Each signal, the verdict it draws, and its outcome share one baseline.
FIX_Y, PROMOTE_Y, REVERT_Y = 126, 214, 270

SIG_X, SIG_W = 8, 140
POL_X, POL_W = 184, 126
POL_Y, POL_H = 88, 214
FIX_X, FIX_W = 344, 326
FIX_BOX_Y, FIX_BOX_H = 84, 108
OUT_X, OUT_W = 722, 170
BOX_X, BOX_Y, BOX_W, BOX_H = 168, 52, 520, 350

SMALL, BODY, TITLE, BIG = 10, 11, 12, 15


def esc(s):
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


def text(x, y, s, size=BODY, fill="ink", weight=None, anchor="start", mono=False):
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


def signal(y, title, sub, color):
    """A signal chip: what the outside world just reported."""
    top = y - 24
    return "".join([
        box(SIG_X, top, SIG_W, 48, fill="panel2"),
        f'<circle cx="{SIG_X + 15}" cy="{y - 7}" r="4" fill="var(--{color})"/>',
        text(SIG_X + 27, y - 3, title, TITLE, "ink", "600"),
        text(SIG_X + 13, y + 15, sub, SMALL, "mid", mono=True),
    ])


def outcome(y, h, title, sub, note=None):
    """An outcome chip. note marks the headline one, in the accent color."""
    top = y - h // 2
    parts = [
        box(OUT_X, top, OUT_W, h, fill="panel2",
            stroke="green" if note else "line", sw=1.5 if note else 1),
        text(OUT_X + 14, top + 21, title, TITLE, "ink", "600"),
        text(OUT_X + 14, top + 38, sub, SMALL, "mid", mono=True),
    ]
    if note:
        parts.append(text(OUT_X + 14, top + 55, note, SMALL, "green", mono=True))
    return "".join(parts)


def svg(pal):
    markers = "".join(
        f'<marker id="a-{c}" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6"'
        f' markerHeight="6" orient="auto-start-reverse">'
        f'<path d="M0,1 L9,5 L0,9 z" fill="var(--{c})"/></marker>'
        for c in ("mid", "faint", "green")
    )
    p = []

    # The boundary that matters: what runs on hardware you own.
    p.append(box(BOX_X, BOX_Y, BOX_W, BOX_H, fill="panel2", stroke="host", rx=10, dash="5 4"))
    p.append(text(BOX_X + 14, BOX_Y + 21, "your host · your keys · nothing phones home",
                  BODY, "host", "600", mono=True))

    # Signals in, from GitHub and Prometheus.
    p.append(signal(FIX_Y, "CI red", "workflow_run failed", "red"))
    p.append(signal(PROMOTE_Y, "DEV green", "smoke probe passed", "green"))
    p.append(signal(REVERT_Y, "prod breach", "p95 over budget", "red"))

    # Policy: the whole point of the picture.
    p.append(box(POL_X, POL_Y, POL_W, POL_H))
    p.append(text(POL_X + 14, POL_Y + 27, "policy", BIG, "ink", "700"))
    p.append(text(POL_X + 14, POL_Y + 46, "decides before", BODY, "mid"))
    p.append(text(POL_X + 14, POL_Y + 61, "anything runs", BODY, "mid"))
    p.append(f'<line x1="{POL_X + 14}" y1="{POL_Y + 76}" x2="{POL_X + POL_W - 14}"'
             f' y2="{POL_Y + 76}" stroke="var(--line)" stroke-width="1"/>')
    for i, s in enumerate(("flap detect", "circuit break", "depends_on", "budgets")):
        p.append(text(POL_X + 14, POL_Y + 97 + i * 17, s, SMALL, "mid", mono=True))

    # What a Fix is. One box, because it is one gated path.
    p.append(box(FIX_X, FIX_BOX_Y, FIX_W, FIX_BOX_H, stroke="blue", sw=1.5))
    p.append(text(FIX_X + 16, FIX_BOX_Y + 25, "Fix", BIG, "ink", "700"))
    p.append(text(FIX_X + 54, FIX_BOX_Y + 25, "the only path to an agent", BODY, "blue", mono=True))
    for i, s in enumerate(("1. a git worktree per Fix",
                           "2. your agent CLI, given the failing logs",
                           "3. the agent commits, xdlc pushes")):
        p.append(text(FIX_X + 16, FIX_BOX_Y + 47 + i * 18, s, BODY, "mid", mono=True))
    p.append(text(FIX_X + 16, FIX_BOX_Y + 99, "claude · codex · cursor · gemini",
                  SMALL, "faint", mono=True))

    # Signal edges into policy.
    for y in (FIX_Y, PROMOTE_Y, REVERT_Y):
        p.append(arrow(SIG_X + SIG_W, y, POL_X))

    # Policy's four verdicts. Only the first reaches the Fix box.
    p.append(arrow(POL_X + POL_W, FIX_Y, FIX_X))
    p.append(text((POL_X + POL_W + FIX_X) // 2, FIX_Y - 9, "fix", BODY, "ink", "700",
                  "middle", mono=True))
    p.append(arrow(POL_X + POL_W, PROMOTE_Y, OUT_X))
    p.append(text(POL_X + POL_W + 12, PROMOTE_Y - 9, "promote", BODY, "ink", "700", mono=True))
    p.append(arrow(POL_X + POL_W, REVERT_Y, OUT_X))
    p.append(text(POL_X + POL_W + 12, REVERT_Y - 9, "revert", BODY, "ink", "700", mono=True))
    p.append(varrow(POL_X + POL_W // 2, POL_Y + POL_H, 330, color="faint", dash="4 4"))
    p.append(text(POL_X + 4, 348, "noop — where most signals end", BODY, "faint", "700", mono=True))

    # A Fix's result leaves the host and lands back on GitHub.
    p.append(arrow(FIX_X + FIX_W, FIX_Y, OUT_X, color="green"))

    p.append(outcome(FIX_Y, 72, "pull request", "the agent's summary", "cost recorded"))
    p.append(outcome(PROMOTE_Y, 50, "develop → main", "fast-forward only"))
    p.append(outcome(REVERT_Y, 50, "main reverted", "before you get paged"))

    # The audit trail, which is what makes any of the above reviewable.
    band_y = 358
    p.append(box(POL_X, band_y, FIX_X + FIX_W - POL_X, 34, fill="panel", rx=6))
    p.append(text(POL_X + 16, band_y + 22,
                  "recorded per action: prompt · output · diff · verdict · cost",
                  BODY, "mid", mono=True))

    # Close the cycle: a merged Fix, a promote and a revert all change the
    # state the next signal is measured against. That is the loop the
    # project is named for, so the picture has to actually loop.
    p.append(f'<path d="M{OUT_X + OUT_W // 2},{FIX_Y - 38} V32 Q{OUT_X + OUT_W // 2},22'
             f' {OUT_X + OUT_W // 2 - 10},22 H{SIG_X + SIG_W // 2 + 10} Q{SIG_X + SIG_W // 2},22'
             f' {SIG_X + SIG_W // 2},32 V{FIX_Y - 31}" fill="none" stroke="var(--faint)"'
             f' stroke-width="1.5" stroke-dasharray="4 4" marker-end="url(#a-faint)"/>')
    p.append(text(W // 2, 16, "the outcome is what the next signal measures", BODY, "faint",
                  anchor="middle", mono=True))

    out = (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}" width="{W}"'
           f' height="{H}" role="img" aria-label="Signals from CI, DEV smoke and prod health'
           f' enter a policy gate that returns Fix, Promote, Revert or noop. Only Fix reaches'
           f' a coding agent, which runs in a per-Fix git worktree on your own host. Every'
           f' action is recorded with its prompt, diff, verdict and cost.">'
           f'<defs>{markers}</defs>{"".join(p)}</svg>')
    for key, value in pal.items():
        out = out.replace(f"var(--{key})", value)
    assert "var(--" not in out, "unmapped color"
    return out


here = pathlib.Path(__file__).parent
for name, pal in (("loop-light.svg", LIGHT), ("loop-dark.svg", DARK)):
    (here / name).write_text(svg(pal))
    print(here / name)
