# Repo-local Go caches so builds work in sandboxed shells that can only
# write inside the repo. GOFLAGS=-mod=mod keeps go.mod/go.sum in sync.
export GOCACHE := $(CURDIR)/.gocache
# -buildvcs=false: VCS stamping writes a stat cache into the global GOMODCACHE,
# which sandboxed shells cannot write to.
export GOFLAGS := -mod=mod -buildvcs=false

.PHONY: build test vet fmt fmtcheck tidy check clean mobile-dev mobile-info mobile-sim desktop-dev

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

mobile-info:
	@contrib/lola-mobile-dev.sh --info

# Build the app and launch it on an iOS Simulator without opening Xcode. The
# Simulator shares the Mac's loopback, so a daemon bound to localhost is reached
# at 127.0.0.1 with no forwarding — which is why this is the fastest loop and
# the first thing to try. LOLA_SIM picks a specific simulator; see the script.
mobile-sim:
	cd mobile && ./scripts/run-sim.sh

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
