# Repo-local Go caches so builds work in sandboxed shells that can only
# write inside the repo. GOFLAGS=-mod=mod keeps go.mod/go.sum in sync.
export GOCACHE := $(CURDIR)/.gocache
# -buildvcs=false: VCS stamping writes a stat cache into the global GOMODCACHE,
# which sandboxed shells cannot write to.
export GOFLAGS := -mod=mod -buildvcs=false

.PHONY: build test vet fmt fmtcheck tidy check clean daemon mobile-dev mobile-lan mobile-info mobile-sim mobile-device desktop-dev

build:
	go build -o lola .

test:
	go test ./...

vet:
	go vet ./...

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

# --- mobile (milestone 1) ---------------------------------------------------
#
# The phone listener only exists in a build tagged lola_insecure, and the binary
# that actually runs is the one on PATH rather than ./lola — so `make build`
# alone can never bring it up. These delegate to one script that both this
# Makefile and mobile/package.json call, so neither side owns the sequence.

mobile-dev:
	@contrib/lola-mobile-dev.sh

# The same, but reachable from a PHYSICAL phone. A separate target rather than
# `make mobile-dev --lan`, which cannot work: make claims a leading `--` for its
# own options and never passes it through.
#
# It binds beyond loopback, so the shared bearer key crosses your network. The
# daemon says so loudly on every start. Note that the TUI's ^r and the desktop
# app's restart button do NOT carry the opt-in, so a restart from either drops
# back to loopback and the phone silently loses its route — come back here.
mobile-lan:
	@contrib/lola-mobile-dev.sh --lan

mobile-info:
	@contrib/lola-mobile-dev.sh --info

# Refresh the daemon this machine actually runs, from COMMITTED HEAD, and leave
# it LAN-reachable for a phone. This is the target to reach for when a phone
# build has a command the running daemon does not: the daemon never hot-reloads
# its own binary, and `make build` writes ./lola, which nothing ever executes.
#
# It differs from mobile-lan in ONE way: the source. mobile-lan installs the
# WORKING TREE, which is right when the change you are testing is uncommitted
# and wrong when somebody else's half-finished work is sitting beside it. This
# builds `git archive HEAD`, so only committed code reaches the operator's
# daemon. Run mobile-lan when you want your own uncommitted change tested.
#
# Runs in the FOREGROUND, like mobile-lan: the daemon logs to this terminal and
# ^C stops it.
daemon:
	@contrib/lola-mobile-dev.sh --lan --head


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
