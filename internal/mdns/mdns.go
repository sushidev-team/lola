// Package mdns advertises the phone listener on the local network with
// `dns-sd`, so a phone can find this Mac by NAME rather than by an address that
// changes every time the laptop moves.
//
// WHY THIS EXISTS. The pairing credentials are already network-independent: the
// bearer key and the SPKI pin identify the DAEMON, and neither mentions an
// address. Only the address goes stale — the connect code carries the addresses
// the machine had at pairing time, so a phone paired at home cannot find the
// same Mac at the office, on a hotspot, or after the router hands out a
// different lease. Discovery closes exactly that gap and nothing else: it finds
// a candidate, and the pin still decides whether to trust it.
//
// WHY `dns-sd` RATHER THAN A LIBRARY. Every Mac ships it, it is the interface
// to the mDNSResponder that is already running, and it keeps this package a
// leaf with no dependency — the same trade the rest of lola makes for tmux,
// git and gh. The cost is a long-lived child process to supervise, which is
// what most of this file is about: `dns-sd -R` holds the registration for as
// long as it runs and withdraws it when it exits.
//
// WHAT IS ADVERTISED, AND WHAT IS NOT. The service type, the port, and a TXT
// record carrying the SPKI pin and a protocol version. The pin is PUBLIC — the
// daemon logs it at startup and it is in the connect code — and publishing it
// is what lets a phone reject an impostor before opening a socket. The BEARER
// KEY is never advertised, never in argv, and never in a TXT record; anything
// on the network can read both.
//
// A FAILURE HERE IS NEVER FATAL. No dns-sd, a refused registration, a child
// that dies — each costs discovery and nothing else, because the stored
// addresses still work and are still tried first. The listener must never
// depend on this.
package mdns

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ServiceType is the DNS-SD service the daemon registers and the phone browses
// for. It is part of the wire contract between them: changing it makes every
// paired phone stop finding this daemon.
const ServiceType = "_lola._tcp"

// Domain is the multicast DNS domain. "local" is the only one this ever uses —
// a wide-area registration would publish a developer's machine to a unicast DNS
// zone, which is not what "find my laptop on this network" means.
const Domain = "local"

// TXT keys. Short, because a TXT record is size-limited and these travel in
// every announcement.
const (
	// TXTPin carries the listener's SPKI pin, so a browsing phone can drop an
	// impostor before it opens a socket. Public by design.
	TXTPin = "pin"
	// TXTVersion is the discovery contract's version, not the wire protocol's.
	TXTVersion = "v"
)

// Version is the current value of the version TXT key.
const Version = "1"

// restartDelay is how long the supervisor waits before restarting a child that
// exited. Long enough that a permanently failing dns-sd cannot spin, short
// enough that a transient failure heals before anyone notices.
//
// A var so the tests can shrink it; nothing in production writes it.
var restartDelay = 5 * time.Second

// Service is what to advertise.
type Service struct {
	// Instance is the human-readable name shown in a browser's list. Not an
	// identity: two machines may share one, which is why the pin is what
	// decides trust.
	Instance string
	Port     int
	// TXT is the record's key/value pairs. Emitted in sorted key order so the
	// argv is reproducible, which is what makes it testable.
	TXT map[string]string
}

// Process is the half of a running `dns-sd` this package needs, so tests can
// supply one that never execs.
type Process interface {
	// Wait blocks until the child exits. A registration lives exactly as long
	// as its child does.
	Wait() error
}

// Starter launches the registration child. The context cancels it: an
// exec.CommandContext kills the process, which is what withdraws the
// registration.
type Starter func(ctx context.Context, bin string, args []string) (Process, error)

// Advertiser holds one registration alive, restarting it if it dies.
//
// The zero value is unusable; call New. Start and Stop may be called from any
// goroutine, and Stop is idempotent — the daemon calls it on shutdown and again
// on every rebind.
type Advertiser struct {
	bin   string
	start Starter
	logf  func(string, ...any)

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// New builds an advertiser. bin defaults to "dns-sd", start to the real exec,
// and logf to a no-op, so a caller supplies only what it wants to change.
func New(bin string, start Starter, logf func(string, ...any)) *Advertiser {
	if bin == "" {
		bin = "dns-sd"
	}
	if start == nil {
		start = execStart
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Advertiser{bin: bin, start: start, logf: logf}
}

// execStart is the production Starter.
func execStart(ctx context.Context, bin string, args []string) (Process, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	// The child's output is status chatter ("Registering Service ... "), not
	// something to parse or relay: what matters is whether it stays running.
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// Args builds the dns-sd argv for a service. Exported for the tests, and
// because it is the whole contract with the tool.
//
//	dns-sd -R <instance> _lola._tcp local <port> key=value ...
func Args(s Service) ([]string, error) {
	instance := sanitizeText(s.Instance)
	if instance == "" {
		return nil, errors.New("mdns: no instance name")
	}
	if s.Port < 1 || s.Port > 65535 {
		return nil, fmt.Errorf("mdns: port %d is out of range", s.Port)
	}
	args := []string{"-R", instance, ServiceType, Domain, strconv.Itoa(s.Port)}
	keys := make([]string, 0, len(s.TXT))
	for k := range s.TXT {
		if sanitizeText(k) != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		// Each pair is ONE argv element, so a value with a space cannot become
		// a second record — but control characters are stripped anyway, since
		// this text ends up in an announcement every device on the network
		// reads.
		args = append(args, sanitizeText(k)+"="+sanitizeText(s.TXT[k]))
	}
	return args, nil
}

// sanitizeText strips control characters and trims. What survives is what a
// DNS-SD record may carry and what an argv element may safely hold.
func sanitizeText(v string) string {
	var b strings.Builder
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// Start advertises the service until Stop, replacing any previous registration.
//
// It returns the error only for a service it cannot describe at all; every
// runtime failure is logged and retried, because a machine without dns-sd must
// keep its listener.
func (a *Advertiser) Start(svc Service) error {
	args, err := Args(svc)
	if err != nil {
		return err
	}
	a.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	a.mu.Lock()
	a.cancel, a.done = cancel, done
	a.mu.Unlock()

	go a.supervise(ctx, args, done)
	return nil
}

// supervise keeps one child alive until the context is cancelled.
//
// A registration that dies silently is the failure mode worth guarding: the
// phone simply stops finding the daemon, with nothing in the log. So an exit is
// reported ONCE per streak and retried on a fixed delay — restarting instantly
// would spin against a permanent failure (no dns-sd, a name conflict) and fill
// the log with it.
func (a *Advertiser) supervise(ctx context.Context, args []string, done chan struct{}) {
	defer close(done)
	reported := false
	for {
		proc, err := a.start(ctx, a.bin, args)
		if err != nil {
			if !reported {
				a.logf("remote: cannot advertise on the local network (%v); phones will use the addresses in their connect code", err)
				reported = true
			}
		} else {
			if err := proc.Wait(); err != nil && ctx.Err() == nil && !reported {
				a.logf("remote: the local-network advertisement stopped (%v); retrying", err)
				reported = true
			}
		}
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(restartDelay):
		}
	}
}

// Stop withdraws the registration and waits for the child to be gone.
//
// Idempotent, and safe on an advertiser that was never started: the daemon
// calls it on every rebind and on shutdown, where "was there one?" is not a
// question worth asking.
func (a *Advertiser) Stop() {
	a.mu.Lock()
	cancel, done := a.cancel, a.done
	a.cancel, a.done = nil, nil
	a.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}
