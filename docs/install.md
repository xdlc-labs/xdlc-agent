# Install

**Audience:** anyone who wants `xdlc` on PATH without cloning the repo.

## Binary (recommended)

Downloads the newest GitHub Release (including prereleases), verifies `checksums.txt`, installs as `~/.local/bin/xdlc`.

```sh
curl -fsSL https://raw.githubusercontent.com/xdlc-labs/xdlc-agent/main/scripts/install.sh | sh
```

Pin a tag:

```sh
curl -fsSL https://raw.githubusercontent.com/xdlc-labs/xdlc-agent/main/scripts/install.sh \
  | XDLC_VERSION=v0.0.1-beta.1 sh
```

Custom install dir:

```sh
curl -fsSL https://raw.githubusercontent.com/xdlc-labs/xdlc-agent/main/scripts/install.sh \
  | XDLC_INSTALL_DIR=/usr/local/bin sh
```

If `~/.local/bin` is not on `PATH`:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

Then:

```sh
xdlc version
xdlc demo --provider fake
```

Linux / macOS, amd64 / arm64. Needs `curl`, `tar`, and `sha256sum` or `shasum`.

## Docker

Image embeds the ops console. Tag must exist on GHCR:

```sh
docker run --rm -p 8080:8080 \
  -v "$PWD/config.yaml:/etc/xdlc-agent/config.yaml:ro" \
  -e XDLC_API_TOKEN -e GITHUB_TOKEN -e GITHUB_WEBHOOK_SECRET \
  -e ANTHROPIC_API_KEY -e OPENAI_API_KEY -e CURSOR_API_KEY \
  ghcr.io/xdlc-labs/xdlc-agent:0.0.1-beta.2 \
  daemon --config /etc/xdlc-agent/config.yaml
```

## From source

```sh
git clone https://github.com/xdlc-labs/xdlc-agent.git
cd xdlc-agent
go build -o bin/xdlc ./cmd/xdlc-agent
```

## Next

- [Getting started](getting-started.md) — demo or CI Fix daemon
- [Optional profiles](production-loop.md) — GitOps / prod later
- [Deployment](deployment.md) — Helm / cluster
- [API tokens](api-tokens.md) — console bearer
