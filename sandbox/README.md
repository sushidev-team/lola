# lola-sandbox image

The default image of lola's [container sandbox](../README.md#the-container-sandbox):
a project with `sandbox = "docker"` and no image configured runs its agent in
`ghcr.io/sushidev-team/lola-sandbox:<lola version>` (`:edge` for a non-release
build).

What is inside (Alpine, `linux/amd64` and `linux/arm64`):

- Claude Code, the native musl build, version pinned by `CLAUDE_VERSION` and
  checked against the release manifest's sha256. Auto-update is off
  (`DISABLE_AUTOUPDATER=1`); a new version comes with a new image.
- git, gh, ripgrep, bash, curl, openssh-client, ca-certificates.
- lola at `/usr/local/bin/lola`, built from the same commit. The daemon copies
  it out once per image ID for the agent's hooks.
- A non-root user `lola` (uid 1000, `HOME=/home/lola`). Claude refuses
  `--dangerously-skip-permissions` as root.
- `/etc/gitconfig`: `safe.directory = *` (the bind-mounted worktree has a host
  owner), GitHub ssh remotes rewritten to https, and `gh` as the credential
  helper for github.com (it uses `GH_TOKEN`). The repository's own config is
  mounted read-only and still applies.

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

## Add your toolchain

Any image works that has `sh`, `git` and the coding agent on `PATH`. It does
not need lola: lola mounts its own binary into every sandboxed container. The
simplest way to add a toolchain is to build on this image:

```dockerfile
FROM ghcr.io/sushidev-team/lola-sandbox:1.2.3
USER root
RUN apk add --no-cache nodejs npm php84 composer
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
