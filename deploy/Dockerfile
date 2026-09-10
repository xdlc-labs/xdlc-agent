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

# Debian/glibc, not Alpine/musl: cursor-agent's bundled node is a glibc
# ELF (`/lib64/ld-linux-x86-64.so.2`) and its launcher is
# `#!/usr/bin/env bash`. Copying the host CLI into an Alpine image
# failed with `required file not found` (shop dogfood F1).
FROM node:22-bookworm-slim
ARG KUBECTL_VERSION
ARG ARGOCD_VERSION
ARG CLAUDE_CODE_VERSION
ARG CODEX_CLI_VERSION
ARG GEMINI_CLI_VERSION
ARG TARGETARCH

USER root
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl git bash \
    && rm -rf /var/lib/apt/lists/* \
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
# installer at https://cursor.com/install. This image is glibc + bash so
# that overlay (COPY the host CLI onto PATH, or run the installer as
# root before USER) actually executes. Still left out of the default
# image rather than pinning Cursor's installer in this Dockerfile.

COPY --from=build /out/xdlc /usr/local/bin/xdlc

# Belt and braces for the same problem the daemon solves in code
# (internal/repos.CommitterEnv): a Fix is delivered by the coding agent
# running `git commit` in its worktree, and git refuses to commit
# without an author. This image runs as uid 65532 with no HOME
# .gitconfig, so without a fallback identity every containerized Fix
# produced no commit and the run was still recorded as clean.
#
# It goes in the *system* config, the lowest-precedence one git reads:
# a mounted ~/.gitconfig, `git config --global`, GIT_AUTHOR_NAME, or
# config.yaml's agent.committer all override it, so an operator using a
# GitHub App bot identity keeps theirs.
RUN printf '[user]\n\tname = xdlc-agent\n\temail = xdlc-agent@users.noreply.github.com\n' > /etc/gitconfig

# Match distroless/static nonroot (65532) used by example-service.
RUN groupadd --gid 65532 nonroot \
 && useradd --uid 65532 --gid 65532 --home-dir /var/lib/xdlc-agent \
      --no-create-home --shell /usr/sbin/nologin nonroot \
 && mkdir -p /var/lib/xdlc-agent \
 && chown -R nonroot:nonroot /var/lib/xdlc-agent

USER nonroot:nonroot
WORKDIR /var/lib/xdlc-agent
ENTRYPOINT ["/usr/local/bin/xdlc"]
CMD ["daemon"]
