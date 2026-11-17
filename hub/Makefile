# Repo-local build cache so builds work in sandboxed shells.
export GOCACHE := $(CURDIR)/.gocache
export GOFLAGS := -buildvcs=false
# The hub has no dependency outside this repository (hubproto is ../hubproto,
# see go.mod), so nothing is ever fetched.
export GOPROXY := off

.PHONY: build vet test check fmt tidy

build:
	go build -o lola-hub ./cmd/lola-hub

vet:
	go vet ./...

test:
	go test ./...

fmt:
	gofmt -l -w .

tidy:
	go mod tidy

check: build vet test
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)
