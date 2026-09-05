**What this changes**

**Checklist**
- [ ] `make build && make test && make lint` clean (or equivalent; do **not** bare `go test ./...` after `ui/` install)
- [ ] `test -z "$(gofmt -l cmd internal services)"`
- [ ] New/changed behavior documented (`docs/`, `README.md`, or code comments)
- [ ] If UI touched: `cd ui && bun run lint && bun run build`
- [ ] Vendor-specific logic (GitHub/ArgoCD/Prometheus/kubectl/coding-agent CLI) stays behind the existing interfaces, no leaking a concrete client into `orchestrator`/`gate`/`subagent` types
