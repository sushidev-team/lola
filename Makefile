# Repo-local Go caches so builds work in sandboxed shells that can only
# write inside the repo. GOFLAGS=-mod=mod keeps go.mod/go.sum in sync.
export GOCACHE := $(CURDIR)/.gocache
# -buildvcs=false: VCS stamping writes a stat cache into the global GOMODCACHE,
# which sandboxed shells cannot write to.
export GOFLAGS := -mod=mod -buildvcs=false

.PHONY: build build-linux sandbox-image test vet fmt fmtcheck tidy check clean daemon daemon-dev mobile-sim mobile-device mobile-wireless desktop-dev

build:
	go build -o lola .

# build-linux cross-compiles the lola that a SANDBOXED agent's hooks run inside
# its container ([sandbox].lola_bin). The container's architecture follows the
# host's (arm64 on Apple silicon); override with GOARCH=amd64.
LINUX_ARCH ?= $(shell go env GOARCH)
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(LINUX_ARCH) go build -o dist/lola-linux-$(LINUX_ARCH) .

# sandbox-image builds the default container sandbox image (sandbox/Dockerfile)
# for this machine's architecture and loads it into the local docker under the
# tag lola runs by default for a non-release build (`:edge`). Release builds
# default to `:<version>`; tag it that way too, or set [sandbox].image, to try a
# local image with a release lola. CI publishes the multi-arch image.
SANDBOX_IMAGE ?= ghcr.io/sushidev-team/lola-sandbox:edge
LOLA_VERSION ?= dev
sandbox-image:
	docker build -f sandbox/Dockerfile --build-arg LOLA_VERSION=$(LOLA_VERSION) -t $(SANDBOX_IMAGE) .

# hubproto is a NESTED module (the hub's wire contract), so ./... skips it;
# it is tested and vetted from its own directory.
test:
	go test ./...
	cd hubproto && go test ./...

vet:
	go vet ./...
	cd hubproto && go vet ./...

# fmtcheck mirrors .github/workflows/ci.yml's gofmt step BYTE FOR BYTE. It lives
# in `check` because CI ran it and `make check` did not, so a file could be
# green locally and fail the build — which is exactly how an unformatted file
# reached main. Reports the offenders and fails; `make fmt` fixes them.
fmtcheck:
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then \
		echo "These files are not gofmt-clean:"; \
		echo "$$out"; \
		echo "run: make fmt"; \
		exit 1; \
	fi

fmt:
	gofmt -w .

tidy:
	GOPROXY=off GOSUMDB=off go mod tidy

check: fmtcheck build vet test

clean:
	rm -rf lola .gocache

# --- mobile / daemon --------------------------------------------------------

# Install the daemon binary this machine runs (the one on PATH), from the
# working tree. The TUI's ^r, the desktop app's restart button and a hand-started
# `lola run` all resolve `lola` from PATH, not ./lola, so `make build` alone
# never reaches them. A running daemon keeps the inode it started with: the new
# build is not live until it restarts.
daemon:
	GOCACHE=$(GOCACHE) GOFLAGS='$(GOFLAGS)' go install .

# The same, then run it in the foreground (^C stops it).
daemon-dev: daemon
	lola run

# Build the app and launch it on an iOS Simulator without opening Xcode. The
# Simulator shares the Mac's loopback, so a daemon bound to localhost is reached
# at 127.0.0.1 with no forwarding — which is why this is the fastest loop and
# the first thing to try. LOLA_SIM picks a specific simulator; see the script.
mobile-sim:
	cd mobile && ./scripts/run-sim.sh

# Build, install and launch on a PHYSICAL iPhone. Three things exist only on
# hardware and all three matter here: the camera (so the QR scanner), the
# local-network permission prompt, and a real network that can actually drop.
# Needs a signing team once — the script says how. See mobile/README.md Â§7.
mobile-device:
	cd mobile && ./scripts/run-device.sh

# Build/install/launch over Wi-Fi after pairing the phone with Xcode once.
# Select a phone by name or UDID with LOLA_DEVICE=Speedy.
mobile-wireless:
	cd mobile && ./scripts/run-device.sh --wireless

# The desktop app's dev loop. `wails3 task build` only refreshes the loose
# bin/Lola, so `open bin/Lola.app` after one launches the OLD bundled binary and
# every source change reads as a no-op; `wails3 dev` runs from source with the
# Web Inspector attached, and regenerates desktop/frontend/bindings whenever a
# bound Go service changes. Package the .app with `wails3 task package` from
# desktop/ when you actually want a bundle.
desktop-dev:
	@command -v wails3 >/dev/null 2>&1 || { \
		echo "wails3 not found. Install it with:"; \
		echo "  go install github.com/wailsapp/wails/v3/cmd/wails3@latest"; \
		echo "(note: a distinct binary from the v2 'wails')"; \
		exit 1; }
	cd desktop && wails3 dev
