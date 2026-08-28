# Repo-local Go caches so builds work in sandboxed shells that can only
# write inside the repo. GOFLAGS=-mod=mod keeps go.mod/go.sum in sync.
export GOCACHE := $(CURDIR)/.gocache
# -buildvcs=false: VCS stamping writes a stat cache into the global GOMODCACHE,
# which sandboxed shells cannot write to.
export GOFLAGS := -mod=mod -buildvcs=false

.PHONY: build test vet fmt fmtcheck tidy check clean remote-dev remote-info

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

remote-dev:
	@contrib/lola-remote-dev.sh

remote-info:
	@contrib/lola-remote-dev.sh --info
