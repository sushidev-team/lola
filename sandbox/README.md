# lola-sandbox image

The default image of lola's [container sandbox](../README.md#the-container-sandbox):
a project with `sandbox = "docker"` and no image configured runs its agent in
`ghcr.io/sushidev-team/lola-sandbox:<lola version>` (`:edge` for a non-release
build).

What is inside (Debian trixie, `linux/amd64` and `linux/arm64`):

- Claude Code, the native glibc build, version pinned by `CLAUDE_VERSION` and
  checked against the release manifest's sha256. Codex (`CODEX_VERSION`) and
  OpenCode (`OPENCODE_VERSION`), installed from npm at pinned versions.
  Auto-update is off for all three; a new version comes with a new image.
- Node (`NODE_VERSION`, copied from the official image) with npm.
- git, gh, ripgrep, jq, bash, curl, unzip, openssh-client, ca-certificates.
- lola at `/usr/local/bin/lola`, built from the same commit. The daemon copies
  it out once per image ID for the agent's hooks.
- A non-root user `lola` (uid 1000, `HOME=/home/lola`). Claude refuses
  `--dangerously-skip-permissions` as root.
- Every package manager caches under `/home/lola/.cache`, which lola mounts as
  one volume per project (`lola-cache-<project>`).
- `/etc/gitconfig`: `safe.directory = *` (the bind-mounted worktree has a host
  owner), GitHub ssh remotes rewritten to https, and `gh` as the credential
  helper for github.com (it uses `GH_TOKEN`). The repository's own config is
  mounted read-only and still applies.

## Project toolchains

A project's other toolchains are added per project, on top of this image
(`internal/toolchain`). lola reads the checkout's files, or the project's
`sandbox_toolchains` when set, and builds a derived image on the first
sandboxed start. It is tagged by content, so it is reused until a toolchain,
version or this image changes.

| Detected from | Adds |
|---|---|
| `composer.json` | PHP from packages.sury.org (version from `config.platform.php` or `require.php`), common extensions plus every `ext-*`, composer |
| `package.json` | another Node major (`.nvmrc`, `engines`), pnpm / yarn / bun (`packageManager`, lockfiles) |
| `go.mod` | Go (the `go` / `toolchain` line) |
| `Cargo.toml` | Rust via rustup, plus the `rust-toolchain.toml` channel |
| `pyproject.toml`, `requirements.txt` | Python 3 and uv |
| `Gemfile` | Ruby and bundler |

```toml
[[project]]
name               = "web"
sandbox            = "docker"
sandbox_toolchains = ["php@8.4", "node"]   # instead of detection; ["none"] adds nothing
sandbox_packages   = ["ffmpeg"]            # extra Debian packages
```

Both keys are ignored when the project names its own image.

## Build it locally

From the repository root:

```sh
make sandbox-image                       # tags ghcr.io/sushidev-team/lola-sandbox:edge
make sandbox-image SANDBOX_IMAGE=lola-sandbox:dev LOLA_VERSION=dev
docker build -f sandbox/Dockerfile --build-arg CLAUDE_VERSION=2.1.311 .
```

The local build is for this machine's architecture. CI
(`.github/workflows/sandbox-image.yml`) publishes both architectures: `:edge`
from `main`, `:<version>` and `:latest` for each release.

## Use your own image

Any image works that has `sh`, `git` and the coding agent on `PATH`. It does
not need lola: lola mounts its own binary into every sandboxed container. The
simplest way is to build on this image:

```dockerfile
FROM ghcr.io/sushidev-team/lola-sandbox:1.2.3
USER root
RUN apt-get update && apt-get install -y --no-install-recommends default-jdk && rm -rf /var/lib/apt/lists/*
USER lola
```

```sh
docker build -t acme/agent-web:1 .
```

Then name it in `~/.lola/config.toml`, for every sandboxed project or for one:

```toml
[sandbox]
image = "acme/agent-web:1"

[[project]]
name          = "web"
sandbox       = "docker"
sandbox_image = "acme/agent-web:1"   # overrides [sandbox].image
```

Keep the final `USER` non-root if the image runs claude: as root it only
accepts skipped permissions because lola sets `IS_SANDBOX=1`.
