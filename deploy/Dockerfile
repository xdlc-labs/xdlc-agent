# Every exec.Command in this codebase (git, the coding-agent CLI,
# argocd, kubectl) needs a real binary on PATH — a distroless/scratch
# image can't run any of it (see docs/architecture.md). This image
# bundles them. Versions are pinned deliberately, not resolved via
# "latest" at build time — bump them here on purpose, not by surprise.
ARG KUBECTL_VERSION=1.34.0
ARG ARGOCD_VERSION=3.5.2
ARG CLAUDE_CODE_VERSION=2.1.260
ARG CODEX_CLI_VERSION=0.153.2
# Gemini CLI is opt-in: build with --build-arg GEMINI_CLI_VERSION=<version>
# to get agent.provider: gemini in this image. Left empty rather than
# pinned to a version this repo has not tested.
ARG GEMINI_CLI_VERSION=

# Ops console static assets → internal/console/dist (go:embed).
FROM oven/bun:1 AS ui
WORKDIR /ui
COPY ui/package.json ui/bun.lock ./
RUN bun install --frozen-lockfile
COPY ui/ .
RUN bun run build

FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Overlay the UI's static SPA build into the embed path when the bun build produced one.
COPY --from=ui /ui/dist /tmp/ui-dist
RUN mkdir -p internal/console/dist \
 && if [ -d /tmp/ui-dist ]; then cp -a /tmp/ui-dist/. internal/console/dist/; fi
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/xdlc ./cmd/xdlc-agent

FROM node:22-alpine
ARG KUBECTL_VERSION
ARG ARGOCD_VERSION
ARG CLAUDE_CODE_VERSION
ARG CODEX_CLI_VERSION
ARG GEMINI_CLI_VERSION
ARG TARGETARCH

RUN apk add --no-cache git ca-certificates curl \
 && curl -fsSL -o /usr/local/bin/kubectl \
      "https://dl.k8s.io/release/v${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl" \
 && chmod +x /usr/local/bin/kubectl \
 && curl -fsSL -o /usr/local/bin/argocd \
      "https://github.com/argoproj/argo-cd/releases/download/v${ARGOCD_VERSION}/argocd-linux-${TARGETARCH}" \
 && chmod +x /usr/local/bin/argocd \
 && npm install -g \
      "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
      "@openai/codex@${CODEX_CLI_VERSION}" \
 && if [ -n "${GEMINI_CLI_VERSION}" ]; then \
      npm install -g "@google/gemini-cli@${GEMINI_CLI_VERSION}"; \
    fi \
 && npm cache clean --force

# Cursor CLI (cursor-agent) isn't npm-installable — it ships via the
# installer at https://cursor.com/install, which puts the binary in a
# per-user home directory rather than a system path. If you need
# agent.provider: cursor in this image, add that install step yourself
# (pointing CURSOR_INSTALL_DIR, or its current equivalent, at
# /usr/local/bin) and confirm it still matches Cursor's current docs —
# left out here rather than guessed at.

COPY --from=build /out/xdlc /usr/local/bin/xdlc

# Match distroless/static nonroot (65532) used by example-service.
RUN addgroup -g 65532 -S nonroot \
 && adduser -u 65532 -S -G nonroot nonroot \
 && mkdir -p /var/lib/xdlc-agent \
 && chown -R nonroot:nonroot /var/lib/xdlc-agent

USER nonroot:nonroot
WORKDIR /var/lib/xdlc-agent
ENTRYPOINT ["/usr/local/bin/xdlc"]
CMD ["daemon"]
