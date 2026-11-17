package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sushidev-team/lola/hub/internal/console"
	"github.com/sushidev-team/lola/hub/internal/store"
	"github.com/sushidev-team/lola/hubproto"
)

func TestDevSetsUpAndServes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data-dev")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	readyc := make(chan devInfo, 1)
	var out, errb bytes.Buffer
	c := &cli{stdout: &out, stderr: &errb, getenv: func(string) string { return "" }, ctx: ctx,
		onReady: func(i devInfo) { readyc <- i }}
	done := make(chan int, 1)
	go func() {
		done <- c.run([]string{"dev", "--data", dir, "--addr", "127.0.0.1:0", "--admin-addr", "127.0.0.1:0", "--org", "team", "--seed-demo", "--demo-repo", "me/app"})
	}()
	var info devInfo
	select {
	case info = <-readyc:
	case code := <-done:
		t.Fatalf("dev exited %d: %s", code, errb.String())
	case <-time.After(10 * time.Second):
		t.Fatal("dev never became ready")
	}

	if !strings.HasPrefix(info.APIURL, "http://127.0.0.1:") || !strings.HasPrefix(info.ConsoleURL, "http://127.0.0.1:") {
		t.Fatalf("urls %q %q", info.APIURL, info.ConsoleURL)
	}
	if len(info.EnrollToken) != 32 || len(info.AdminToken) != 64 || len(info.Seeded) != 3 {
		t.Fatalf("info %+v", info)
	}
	tokRaw, err := os.ReadFile(filepath.Join(dir, console.TokenFile))
	if err != nil || strings.TrimSpace(string(tokRaw)) != info.AdminToken {
		t.Fatalf("admin token file: %v", err)
	}

	// The API answers and the printed token enrolls a device.
	pub, priv, _ := hubproto.GenerateKey()
	body, _ := json.Marshal(hubproto.EnrollRequest{Token: info.EnrollToken, DeviceName: "dev laptop", PublicKey: hubproto.EncodePublicKey(pub)})
	req, _ := http.NewRequest(http.MethodPost, info.APIURL+hubproto.PathEnroll, bytes.NewReader(body))
	if err := hubproto.Sign(req, hubproto.DeviceID(pub), priv, body); err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	rb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(rb), `"team"`) {
		t.Fatalf("enroll: %d %s", resp.StatusCode, rb)
	}
	// The console answers on its own listener.
	resp, err = http.Get(info.ConsoleURL + "/login")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("console: %v", err)
	}
	resp.Body.Close()

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("dev exit %d: %s", code, errb.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("dev did not stop")
	}
	printed := out.String()
	for _, want := range []string{info.APIURL, info.EnrollToken, info.ConsoleURL, info.AdminToken, "lola hub join " + info.APIURL, "Team hub"} {
		if !strings.Contains(printed, want) {
			t.Fatalf("dev output lacks %q:\n%s", want, printed)
		}
	}
	st, err := store.Open(filepath.Join(dir, "hub.json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = st.View(func(d *store.Data) error {
		if d.Orgs["team"] == nil || len(d.Items) != 3 {
			t.Fatalf("store: orgs %v items %d", d.Orgs, len(d.Items))
		}
		for _, w := range d.Items {
			if w.Item.Repo() != "me/app" || hubproto.Validate(w.Item) != nil {
				t.Fatalf("seed item %+v", w.Item)
			}
		}
		return nil
	})

	// A second start reuses the org, the store and the admin token, and
	// seeds nothing without --seed-demo.
	ctx2, cancel2 := context.WithCancel(context.Background())
	c2 := &cli{stdout: io.Discard, stderr: io.Discard, getenv: func(string) string { return "" }, ctx: ctx2,
		onReady: func(i devInfo) { readyc <- i; cancel2() }}
	if code := c2.run([]string{"dev", "--data", dir, "--addr", "127.0.0.1:0", "--admin-addr", "127.0.0.1:0", "--org", "team"}); code != 0 {
		t.Fatalf("second start %d", code)
	}
	if again := <-readyc; again.AdminToken != info.AdminToken || again.EnrollToken == info.EnrollToken || len(again.Seeded) != 0 {
		t.Fatalf("second start %+v", again)
	}
}

func TestConsoleRefusesNonLoopback(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{
		{"serve", "--addr", "127.0.0.1:0", "--admin-addr", "0.0.0.0:8098"},
		{"serve", "--addr", "127.0.0.1:0", "--admin-addr", ":8098"},
		{"dev", "--data", h.dir, "--admin-addr", "192.168.1.10:8098"},
	} {
		if errs := h.fails(args...); !strings.Contains(errs, "loopback") {
			t.Fatalf("%v: %s", args, errs)
		}
	}
	if _, err := os.Stat(filepath.Join(h.dir, console.TokenFile)); err == nil {
		t.Fatal("refused start still wrote a token")
	}
}
