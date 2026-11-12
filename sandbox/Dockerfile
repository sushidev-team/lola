# syntax=docker/dockerfile:1
#
# The default image of lola's container sandbox ([[project]].sandbox = "docker").
# Published as ghcr.io/sushidev-team/lola-sandbox:<version> (and :edge from main).
# Build locally with `make sandbox-image`; the build context is the repository
# root. See sandbox/README.md.

ARG ALPINE_VERSION=3.22
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
FROM --platform=$BUILDPLATFORM alpine:${ALPINE_VERSION} AS claude
ARG TARGETARCH
ARG CLAUDE_VERSION=2.1.311
RUN apk add --no-cache curl jq
RUN set -eu; \
    case "$TARGETARCH" in \
      amd64) platform=linux-x64-musl ;; \
      arm64) platform=linux-arm64-musl ;; \
      *) echo "unsupported arch $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    base="https://downloads.claude.ai/claude-code-releases/${CLAUDE_VERSION}"; \
    sum=$(curl -fsSL "$base/manifest.json" | jq -r --arg p "$platform" '.platforms[$p].checksum // empty'); \
    echo "$sum" | grep -Eq '^[a-f0-9]{64}$' || { echo "no checksum for $platform in the manifest" >&2; exit 1; }; \
    curl -fsSL -o /claude "$base/$platform/claude"; \
    echo "$sum  /claude" | sha256sum -c -; \
    chmod 0755 /claude

# --- runtime -----------------------------------------------------------------
FROM alpine:${ALPINE_VERSION}
ARG LOLA_VERSION=dev
ARG CLAUDE_VERSION=2.1.311
LABEL org.opencontainers.image.title="lola-sandbox" \
      org.opencontainers.image.description="Default container for lola's sandboxed coding agents: claude, git, gh, ripgrep" \
      org.opencontainers.image.source="https://github.com/sushidev-team/lola" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${LOLA_VERSION}" \
      dev.sushi.lola.claude-version="${CLAUDE_VERSION}"
# libgcc + libstdc++: claude's musl build links them dynamically.
RUN apk add --no-cache bash ca-certificates curl git github-cli libgcc libstdc++ openssh-client ripgrep \
 && adduser -D -u 1000 -h /home/lola -s /bin/bash lola
COPY sandbox/gitconfig /etc/gitconfig
COPY --from=claude /claude /usr/local/bin/claude
COPY --from=lola /out/lola /usr/local/bin/lola
# The image is immutable: claude must not try to update itself, and it uses the
# system ripgrep rather than the one it would otherwise unpack.
ENV DISABLE_AUTOUPDATER=1 \
    USE_BUILTIN_RIPGREP=0
# claude refuses --dangerously-skip-permissions as root.
USER lola
WORKDIR /home/lola
CMD ["bash"]
