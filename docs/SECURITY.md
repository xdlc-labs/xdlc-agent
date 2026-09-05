# Security

## Reporting

Report privately via [GitHub security advisories](https://github.com/xdlc-labs/xdlc-agent/security/advisories/new). Do not post exploit details in public issues.

## Trust model (short)

`xdlc daemon` is privileged by design: it can push to repos, open PRs, and call your coding-agent CLI. Run it in a locked-down namespace, prefer a GitHub App with least privilege, and never put long-lived secrets in `config.yaml` (use env refs — see `config.example.yaml`).

Subagents inherit a tight env allowlist; treat CI logs and branch names as untrusted input (prompt framing already marks them UNTRUSTED).
