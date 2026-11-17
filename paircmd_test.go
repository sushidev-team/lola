package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sushidev-team/lola/internal/protocol"
)

type pairFake struct {
	mu      sync.Mutex
	sent    []protocol.Request
	confirm []func() (*protocol.Response, error) // one per pairConfirm
	status  protocol.PairStatusData
}

func pairData(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func (f *pairFake) install(t *testing.T) {
	t.Helper()
	oldSend, oldTTY := pairSend, pairStdoutTTY
	t.Cleanup(func() { pairSend, pairStdoutTTY = oldSend, oldTTY })
	pairStdoutTTY = func() (bool, int) { return false, 0 }
	pairSend = func(req protocol.Request) (*protocol.Response, error) {
		f.mu.Lock()
		f.sent = append(f.sent, req)
		f.mu.Unlock()
		switch req.Cmd {
		case protocol.CmdPairBegin:
			return &protocol.Response{OK: true, Data: pairData(protocol.PairBeginData{Pairing: "p1", Payload: "lola2.PAYLOAD"})}, nil
		case protocol.CmdPairStatus:
			if f.status.State == protocol.PairStateConfirming {
				return &protocol.Response{OK: true, Data: pairData(f.status)}, nil
			}
			time.Sleep(time.Millisecond)
			return &protocol.Response{OK: true, Data: pairData(protocol.PairStatusData{Pairing: "p1", State: protocol.PairStateWaiting})}, nil
		case protocol.CmdPairConfirm:
			f.mu.Lock()
			next := f.confirm[0]
			f.confirm = f.confirm[1:]
			f.mu.Unlock()
			return next()
		case protocol.CmdPairReject:
			return &protocol.Response{OK: true, Data: pairData(protocol.PairStatusData{State: protocol.PairStateRejected})}, nil
		}
		return nil, errors.New("unexpected " + req.Cmd)
	}
}

func (f *pairFake) cmds() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.sent {
		out = append(out, r.Cmd)
	}
	return out
}

func (f *pairFake) last(cmd string) protocol.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.sent) - 1; i >= 0; i-- {
		if f.sent[i].Cmd == cmd {
			return f.sent[i]
		}
	}
	return protocol.Request{}
}

func paired(name string) func() (*protocol.Response, error) {
	return func() (*protocol.Response, error) {
		return &protocol.Response{OK: true, Data: pairData(protocol.PairStatusData{State: protocol.PairStatePaired, Device: &protocol.DeviceInfo{Name: name}})}, nil
	}
}

func confirming(via string) protocol.PairStatusData {
	return protocol.PairStatusData{Pairing: "p1", State: protocol.PairStateConfirming, DeviceName: "Pixel", Via: via}
}

func exitCode(err error) int {
	var ex sshExitError
	if errors.As(err, &ex) {
		return ex.code
	}
	if err != nil {
		return -1
	}
	return 0
}

func TestPairCmdConfirmsWithTypedCode(t *testing.T) {
	f := &pairFake{status: confirming("lan"), confirm: []func() (*protocol.Response, error){paired("Pixel")}}
	f.install(t)
	var out, errb bytes.Buffer
	err := runPair(context.Background(), pairOptions{tiers: []string{"read", "answer"}, name: "Mine"}, strings.NewReader("123456\n"), &out, &errb)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	var a protocol.PairConfirmArgs
	if err := json.Unmarshal(f.last(protocol.CmdPairConfirm).Args, &a); err != nil {
		t.Fatal(err)
	}
	if a.Code != "123456" || a.Pairing != "p1" || a.Name != "Mine" || len(a.Tiers) != 2 {
		t.Fatalf("confirm args = %+v", a)
	}
	if !strings.Contains(out.String(), "Paired Pixel.") || !strings.Contains(out.String(), `"Pixel" wants to connect.`) {
		t.Fatalf("out = %q", out.String())
	}
	if !strings.Contains(out.String(), "lola2.PAYLOAD") {
		t.Fatalf("non-tty stdout carries the payload: %q", out.String())
	}
}

func TestPairCmdEmptyLineRejects(t *testing.T) {
	f := &pairFake{status: confirming("lan")}
	f.install(t)
	var out, errb bytes.Buffer
	err := runPair(context.Background(), pairOptions{}, strings.NewReader("\n"), &out, &errb)
	if exitCode(err) != 1 {
		t.Fatalf("err = %v", err)
	}
	if cmds := f.cmds(); cmds[len(cmds)-1] != protocol.CmdPairReject {
		t.Fatalf("cmds = %v", cmds)
	}
	for _, c := range f.cmds() {
		if c == protocol.CmdPairConfirm {
			t.Fatal("an empty line must never confirm")
		}
	}
}

func TestPairCmdRetriesWrongCode(t *testing.T) {
	f := &pairFake{status: confirming("lan"), confirm: []func() (*protocol.Response, error){
		func() (*protocol.Response, error) {
			return &protocol.Response{Error: "the code does not match the phone; 2 tries left"}, nil
		},
		paired("Pixel"),
	}}
	f.install(t)
	var out, errb bytes.Buffer
	if err := runPair(context.Background(), pairOptions{}, strings.NewReader("111111\n222222\n"), &out, &errb); err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(out.String(), "That code does not match; 2 tries left.") || !strings.Contains(out.String(), "Paired Pixel.") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestPairCmdNeverPrintsCode(t *testing.T) {
	f := &pairFake{status: confirming("lan"), confirm: []func() (*protocol.Response, error){
		func() (*protocol.Response, error) {
			return &protocol.Response{Error: "the code does not match the phone; 0 tries left, the pairing has failed"}, nil
		},
	}}
	f.install(t)
	var out, errb bytes.Buffer
	err := runPair(context.Background(), pairOptions{json: true}, strings.NewReader("987654\n"), &out, &errb)
	if exitCode(err) != 1 {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(out.String()+errb.String(), "987654") {
		t.Fatalf("the typed code was printed: %q %q", out.String(), errb.String())
	}
	if !strings.Contains(out.String(), `"event":"begin"`) || !strings.Contains(out.String(), `"event":"status"`) {
		t.Fatalf("json events missing: %q", out.String())
	}
}

func TestPairCmdRejectsOnSIGINT(t *testing.T) {
	f := &pairFake{status: confirming("lan")}
	f.install(t)
	pr, pw := io.Pipe()
	t.Cleanup(func() { pw.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	var out, errb bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- runPair(ctx, pairOptions{}, pr, &out, &errb) }()
	deadline := time.After(2 * time.Second)
	for len(f.cmds()) < 2 {
		select {
		case <-deadline:
			t.Fatal("pairing never reached the prompt")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if exitCode(err) != 130 {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("interrupt did not return")
	}
	if cmds := f.cmds(); cmds[len(cmds)-1] != protocol.CmdPairReject {
		t.Fatalf("cmds = %v", cmds)
	}
}

func TestPairCmdShowsViaRelay(t *testing.T) {
	f := &pairFake{status: confirming("relay")}
	f.install(t)
	var out, errb bytes.Buffer
	_ = runPair(context.Background(), pairOptions{}, strings.NewReader("\n"), &out, &errb)
	if !strings.Contains(out.String(), `"Pixel" (via relay) wants to connect.`) {
		t.Fatalf("out = %q", out.String())
	}
	if got := pairPrompt(protocol.PairStatusData{DeviceName: "Pixel", Replaces: "d1"}); !strings.HasPrefix(got, "New device: ") || !strings.Contains(got, "(re-pairs d1)") {
		t.Fatalf("prompt = %q", got)
	}
}

func TestDevicesCmdResolvesName(t *testing.T) {
	old := pairSend
	t.Cleanup(func() { pairSend = old })
	devs := []protocol.DeviceInfo{
		{ID: "d1", Name: "Pixel", Tiers: []string{"read"}},
		{ID: "d2", Name: "Twin", Tiers: []string{"read"}},
		{ID: "d3", Name: "Twin", Tiers: []string{"read"}},
	}
	var sent []protocol.Request
	pairSend = func(req protocol.Request) (*protocol.Response, error) {
		sent = append(sent, req)
		switch req.Cmd {
		case protocol.CmdDevices:
			return &protocol.Response{OK: true, Data: pairData(protocol.DevicesData{Devices: devs})}, nil
		case protocol.CmdRevokeDevice:
			return &protocol.Response{OK: true, Data: pairData(protocol.RevokeDeviceData{ID: "d1", Closed: 2})}, nil
		}
		return nil, errors.New("unexpected " + req.Cmd)
	}
	run := func(args ...string) (string, error) {
		c := devicesCmd()
		var b bytes.Buffer
		c.SetOut(&b)
		c.SetErr(&b)
		c.SetArgs(args)
		err := c.Execute()
		return b.String(), err
	}
	out, err := run("revoke", "Pixel")
	if err != nil || !strings.Contains(out, "Revoked Pixel; closed 2 connection(s).") {
		t.Fatalf("revoke: %q %v", out, err)
	}
	var a protocol.RevokeDeviceArgs
	_ = json.Unmarshal(sent[len(sent)-1].Args, &a)
	if a.ID != "d1" {
		t.Fatalf("revoked %q", a.ID)
	}
	if _, err := run("revoke", "Twin"); err == nil || !strings.Contains(err.Error(), "d2, d3") {
		t.Fatalf("ambiguous name: %v", err)
	}
	if out, err := run(); err != nil || !strings.Contains(out, "NAME") || !strings.Contains(out, "CONNECTED") {
		t.Fatalf("table: %q %v", out, err)
	}
}
