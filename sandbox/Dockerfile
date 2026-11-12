# syntax=docker/dockerfile:1
#
# The default image of lola's container sandbox ([[project]].sandbox = "docker").
# Published as ghcr.io/sushidev-team/lola-sandbox:<version> (and :edge from main).
# Build locally with `make sandbox-image`; the build context is the repository
# root. See sandbox/README.md.

ARG DEBIAN_VERSION=trixie
ARG NODE_VERSION=24
ARG GO_VERSION=1.25

# --- lola (linux, static) ----------------------------------------------------
# Cross-compiled on the build platform: Go needs no emulation for this.
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS lola
ARG TARGETOS
ARG TARGETARCH
ARG LOLA_VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
COPY hubproto/go.mod hubproto/
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=${LOLA_VERSION}" -o /out/lola .

# --- Claude Code (native musl build, checksum-verified) ----------------------
# The same verification the official installer does: the release manifest's
# sha256 for this platform must match the downloaded binary.
# --- claude (glibc) ----------------------------------------------------------
FROM --platform=$BUILDPLATFORM debian:${DEBIAN_VERSION}-slim AS claude
ARG TARGETARCH
ARG CLAUDE_VERSION=2.1.311
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl jq && rm -rf /var/lib/apt/lists/*
RUN set -eu; \
    case "$TARGETARCH" in \
      amd64) platform=linux-x64 ;; \
      arm64) platform=linux-arm64 ;; \
      *) echo "unsupported arch $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    base="https://downloads.claude.ai/claude-code-releases/${CLAUDE_VERSION}"; \
    sum=$(curl -fsSL "$base/manifest.json" | jq -r --arg p "$platform" '.platforms[$p].checksum // empty'); \
    echo "$sum" | grep -Eq '^[a-f0-9]{64}$' || { echo "no checksum for $platform in the manifest" >&2; exit 1; }; \
    curl -fsSL -o /claude "$base/$platform/claude"; \
    echo "$sum  /claude" | sha256sum -c -; \
    chmod 0755 /claude

# --- node (the official image's build, copied) --------------------------------
FROM node:${NODE_VERSION}-${DEBIAN_VERSION}-slim AS node

# --- runtime -----------------------------------------------------------------
# Debian, not Alpine: prebuilt toolchains (rustup, npm native modules, PHP
# packages, Playwright) expect glibc. Projects add their own toolchains on top
# of this image (internal/toolchain builds a derived image per project).
FROM debian:${DEBIAN_VERSION}-slim
ARG LOLA_VERSION=dev
ARG CLAUDE_VERSION=2.1.311
ARG CODEX_VERSION=0.167.2
ARG OPENCODE_VERSION=1.19.5
LABEL org.opencontainers.image.title="lola-sandbox" \
      org.opencontainers.image.description="Default container for lola's sandboxed coding agents: claude, codex, opencode, git, gh, ripgrep, node" \
      org.opencontainers.image.source="https://github.com/sushidev-team/lola" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${LOLA_VERSION}" \
      dev.sushi.lola.claude-version="${CLAUDE_VERSION}" \
      dev.sushi.lola.codex-version="${CODEX_VERSION}" \
      dev.sushi.lola.opencode-version="${OPENCODE_VERSION}"
RUN apt-get update \
 && apt-get install -y --no-install-recommends bash ca-certificates curl gh git jq less openssh-client procps ripgrep unzip xz-utils \
 && rm -rf /var/lib/apt/lists/* \
 && useradd -m -u 1000 -s /bin/bash lola \
 && install -d -o lola -g lola /home/lola/.cache
COPY sandbox/gitconfig /etc/gitconfig
COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
 && ln -s ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx \
 && if [ -e /usr/local/lib/node_modules/corepack ]; then ln -s ../lib/node_modules/corepack/dist/corepack.js /usr/local/bin/corepack; fi \
 && npm install -g --no-fund --no-audit "@openai/codex@${CODEX_VERSION}" "opencode-ai@${OPENCODE_VERSION}" \
 && npm cache clean --force
COPY --from=claude /claude /usr/local/bin/claude
COPY --from=lola /out/lola /usr/local/bin/lola
# The image is immutable: the agents must not try to update themselves, and
# claude uses the system ripgrep rather than the one it would otherwise unpack.
# Every package manager caches under ~/.cache, which lola mounts as a volume
# per project, so installs are not repeated in every session.
ENV DISABLE_AUTOUPDATER=1 \
    USE_BUILTIN_RIPGREP=0 \
    OPENCODE_DISABLE_AUTOUPDATE=1 \
    XDG_CACHE_HOME=/home/lola/.cache \
    npm_config_cache=/home/lola/.cache/npm \
    npm_config_store_dir=/home/lola/.cache/pnpm-store \
    COMPOSER_CACHE_DIR=/home/lola/.cache/composer \
    GOMODCACHE=/home/lola/.cache/go-mod \
    CARGO_HOME=/home/lola/.cache/cargo \
    PATH=/home/lola/.cache/cargo/bin:/usr/local/cargo/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
# claude refuses --dangerously-skip-permissions as root.
USER lola
WORKDIR /home/lola
CMD ["bash"]
