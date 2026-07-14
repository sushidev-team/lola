# Repo-local Go caches so builds work in sandboxed shells that can only
# write inside the repo. GOFLAGS=-mod=mod keeps go.mod/go.sum in sync.
export GOCACHE := $(CURDIR)/.gocache
# -buildvcs=false: VCS stamping writes a stat cache into the global GOMODCACHE,
# which sandboxed shells cannot write to.
export GOFLAGS := -mod=mod -buildvcs=false

.PHONY: build test vet tidy check clean

build:
	go build -o lola .

test:
	go test ./...

vet:
	go vet ./...

tidy:
	GOPROXY=off GOSUMDB=off go mod tidy

check: build vet test

clean:
	rm -rf lola .gocache
