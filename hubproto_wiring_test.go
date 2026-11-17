package main

import (
	"testing"

	"github.com/sushidev-team/lola/hubproto"
)

// TestHubprotoIsWired pins the root module's `replace ./hubproto`: the nested
// contract module must resolve from this checkout. It also keeps the require
// alive through `go mod tidy` until the daemon's hub client imports it.
func TestHubprotoIsWired(t *testing.T) {
	if hubproto.MaxBody != 64<<10 || !hubproto.KindDepAdvisory.Valid() {
		t.Fatal("hubproto contract changed underneath lola")
	}
}
