# Security

## Reporting

Report privately via [GitHub security advisories](https://github.com/xdlc-labs/xdlc-agent/security/advisories/new). Do not post exploit details in public issues.

## Trust model (short)

`xdlc daemon` is privileged by design: it can push to repos, open PRs, and call your coding-agent CLI. Run it in a locked-down namespace, prefer a GitHub App with least privilege, and never put long-lived secrets in `config.yaml` (use env refs — see `config.example.yaml`).

Subagents inherit a tight env allowlist; treat CI logs and branch names as untrusted input (prompt framing already marks them UNTRUSTED).

**Fix sessions on disk.** With `agent.sessions.enabled` (default true) each Fix writes its prompt, the agent's output and the resulting diff under `sessions/`. Those files are **not scrubbed**: a prompt embeds CI logs, and CI logs occasionally embed leaked secrets. Directories are `0700` and files `0600`, nothing is uploaded, and no API endpoint serves them — but anyone with read access to the daemon host can read them, the same boundary as `BACKLOG.md` and the audit DB. Set `enabled: false` to record nothing. See [Fix sessions](sessions.md).
