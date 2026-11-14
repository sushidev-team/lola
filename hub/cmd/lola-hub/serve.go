package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/sushidev-team/lola/hub/internal/admin"
	"github.com/sushidev-team/lola/hub/internal/api"
	"github.com/sushidev-team/lola/hub/internal/console"
	"github.com/sushidev-team/lola/hub/internal/escalate"
	"github.com/sushidev-team/lola/hub/internal/lease"
	"github.com/sushidev-team/lola/hub/internal/secret"
	"github.com/sushidev-team/lola/hubproto"
)

// hubOpts configures one running hub: the API and, when adminAddr is set,
// the loopback-only admin console.
type hubOpts struct {
	addr, adminAddr, data string
	cert, key, team       string
	ttl                   time.Duration
	escAfter              int
	sweep                 time.Duration
	maxLeaseAge           time.Duration // 0 = hubproto.DefaultMaxLeaseAge
	staleAfter            time.Duration // 0 = hubproto.DefaultStaleAfter
	// secretKey, when set, replaces the environment's secret key (dev keeps
	// one in its data directory so a local hub works in one command).
	secretKey string
	// openTeams lets anyone who reaches the API create a team without a
	// create code, up to maxTeams teams (0 = api.DefaultMaxOrgs).
	openTeams bool
	maxTeams  int
}

// hubReady describes a hub that is listening.
type hubReady struct {
	APIURL       string
	ConsoleURL   string // "" = console off
	AdminToken   string
	TokenCreated bool
	TokenPath    string
}

func (c *cli) serve(args []string) error {
	fs := c.newFlags("serve")
	var o hubOpts
	fs.StringVar(&o.addr, "addr", ":8080", "listen address")
	fs.StringVar(&o.adminAddr, "admin-addr", "", "admin console address, LOOPBACK ONLY (e.g. 127.0.0.1:8098); off when empty")
	data := c.dataFlag(fs)
	fs.StringVar(&o.cert, "tls-cert", "", "TLS certificate (PEM); omit when a proxy terminates TLS")
	fs.StringVar(&o.key, "tls-key", "", "TLS private key (PEM)")
	fs.StringVar(&o.team, "linear-team", c.getenv(escalate.EnvTeam), "Linear team id for a single-org hub whose org set none")
	fs.DurationVar(&o.ttl, "lease-ttl", lease.DefaultTTL, "lease length")
	fs.IntVar(&o.escAfter, "escalate-after", lease.DefaultEscalateAfter, "failed attempts before escalation (an org policy overrides)")
	fs.DurationVar(&o.sweep, "sweep", time.Minute, "lease expiry sweep interval")
	fs.DurationVar(&o.maxLeaseAge, "max-lease-age", hubproto.DefaultMaxLeaseAge, "how long renewals may keep one claim alive")
	fs.DurationVar(&o.staleAfter, "stale-after", hubproto.DefaultStaleAfter, "close an open item not re-reported this long (while its repo is still reported)")
	fs.BoolVar(&o.openTeams, "open-teams", false, "let anyone who can reach this hub create a team WITHOUT a create code (private networks only)")
	fs.IntVar(&o.maxTeams, "max-teams", api.DefaultMaxOrgs, "with --open-teams: the most teams the hub holds before creation needs a create code again")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if err := want(pos, 0, ""); err != nil {
		return err
	}
	o.data = *data
	ctx, stop := c.context()
	defer stop()
	return c.runHub(ctx, o, func(r hubReady) {
		if r.ConsoleURL == "" {
			return
		}
		fmt.Fprintf(c.stderr, "admin console: %s (loopback only)\n", r.ConsoleURL)
		// The path only, never the token: stderr is also the server log, so
		// under Docker or systemd a printed token would sit in docker logs or
		// journald for anyone who can read them. (lola-hub dev prints it to
		// stdout for local testing.)
		state := "in"
		if r.TokenCreated {
			state = "created in"
		}
		fmt.Fprintf(c.stderr, "admin token: %s %s (read it there to sign in)\n", state, r.TokenPath)
	})
}

// context is the process lifetime: SIGINT/SIGTERM, or the test's.
func (c *cli) context() (context.Context, context.CancelFunc) {
	if c.ctx != nil {
		return context.WithCancel(c.ctx)
	}
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func (c *cli) runHub(ctx context.Context, o hubOpts, ready func(hubReady)) error {
	if (o.cert == "") != (o.key == "") {
		return usagef("--tls-cert and --tls-key go together")
	}
	if o.ttl < time.Minute || o.escAfter < 1 || o.sweep < time.Second {
		return usagef("--lease-ttl >= 1m, --escalate-after >= 1, --sweep >= 1s")
	}
	if o.maxLeaseAge < 0 || (o.maxLeaseAge > 0 && o.maxLeaseAge < o.ttl) || o.staleAfter < 0 || (o.staleAfter > 0 && o.staleAfter < time.Hour) {
		return usagef("--max-lease-age >= --lease-ttl, --stale-after >= 1h")
	}
	if o.maxTeams < 0 {
		return usagef("--max-teams >= 1")
	}
	if o.adminAddr != "" {
		// Before anything else listens or the store is touched: a console on
		// a public interface is refused outright.
		if err := console.CheckLoopback(o.adminAddr); err != nil {
			return err
		}
	}
	st, err := openStore(o.data)
	if err != nil {
		return err
	}
	logger := log.New(c.stderr, "lola-hub ", log.LstdFlags|log.LUTC)

	// The secret key seals every org's Linear key and digest webhook. It is
	// read once and dropped from the environment; it is never logged, never
	// part of argv and never written to the store.
	sk := o.secretKey
	if sk == "" {
		sk = c.getenv(secret.EnvKey)
	}
	_ = os.Unsetenv(secret.EnvKey)
	box, err := secret.New(sk)
	if err != nil {
		return err
	}
	if box == nil {
		logger.Printf("no %s: the hub stores no org's Linear key or digest webhook; escalations stay pending", secret.EnvKey)
	}
	// An older deployment kept ONE Linear key and ONE webhook in the
	// environment. They are adopted into the hub's only org, sealed, and
	// read from the environment no more. Fail open: a hub that cannot adopt
	// them still serves.
	legacy := admin.LegacyEnv{LinearKey: c.getenv(escalate.EnvKey), DigestWebhook: c.getenv(escalate.EnvDigest)}
	_ = os.Unsetenv(escalate.EnvKey)
	_ = os.Unsetenv(escalate.EnvDigest)
	if legacy != (admin.LegacyEnv{}) {
		legacy.LinearTeam = o.team
		adopted, err := admin.AdoptLegacyEnv(st, box, legacy, time.Now())
		for _, what := range adopted {
			logger.Printf("adopted the hub-wide %s into the hub's only org", what)
		}
		if err != nil {
			logger.Printf("%s / %s are no longer read at runtime and could not be adopted: %v", escalate.EnvKey, escalate.EnvDigest, err)
		} else {
			logger.Printf("%s and %s can be removed from the environment now: each org holds its own", escalate.EnvKey, escalate.EnvDigest)
		}
	}
	esc := &escalate.Escalator{Store: st, Box: box, Team: o.team, Logf: logger.Printf}
	digest := &escalate.Digester{Store: st, Box: box, Logf: logger.Printf}

	srv := api.New(api.Config{Store: st, LeaseTTL: o.ttl, EscalateAfter: o.escAfter,
		MaxLeaseAge: o.maxLeaseAge, StaleAfter: o.staleAfter, Logf: logger.Printf,
		Admin:     &admin.Ops{Store: st, Box: box},
		OpenTeams: o.openTeams, MaxOrgs: o.maxTeams})
	if o.openTeams {
		logger.Printf("--open-teams: anyone who can reach this hub can create a team without a create code")
	}
	hs := &http.Server{
		Handler:           accessLog(logger, srv.Handler()),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          logger,
	}
	ln, err := net.Listen("tcp", o.addr)
	if err != nil {
		return err
	}
	scheme := "http"
	if o.cert != "" {
		scheme = "https"
	}
	info := hubReady{APIURL: scheme + "://" + displayAddr(ln.Addr())}

	var cs *http.Server
	var cln net.Listener
	if o.adminAddr != "" {
		dir := filepath.Dir(o.data)
		tok, created, err := console.LoadOrCreateToken(dir)
		if err != nil {
			ln.Close()
			return err
		}
		con, err := console.New(console.Config{Store: st, Token: tok, Logf: logger.Printf,
			Box: box, LinearTeam: o.team})
		if err != nil {
			ln.Close()
			return err
		}
		if cln, err = console.Listen(o.adminAddr); err != nil {
			ln.Close()
			return err
		}
		cs = &http.Server{
			Handler:           accessLog(logger, con.Handler()),
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       2 * time.Minute,
			MaxHeaderBytes:    16 << 10,
			ErrorLog:          logger,
		}
		info.ConsoleURL = "http://" + displayAddr(cln.Addr())
		info.AdminToken, info.TokenCreated = tok, created
		info.TokenPath = filepath.Join(dir, console.TokenFile)
	}

	go func() {
		t := time.NewTicker(o.sweep)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if n, err := srv.Sweep(); err != nil {
					logger.Printf("sweep: %v", err)
				} else if n > 0 {
					logger.Printf("sweep: reopened %d expired lease(s)", n)
				}
			}
		}
	}()
	go esc.Run(ctx, time.Minute)
	go digest.Run(ctx, escalate.DigestInterval)

	errc := make(chan error, 2)
	go func() {
		logger.Printf("listening on %s (data %s, tls %v, org secrets %v)", ln.Addr(), o.data, o.cert != "", box != nil)
		if o.cert != "" {
			errc <- hs.ServeTLS(ln, o.cert, o.key)
		} else {
			errc <- hs.Serve(ln)
		}
	}()
	if cs != nil {
		go func() { errc <- cs.Serve(cln) }()
	}
	if ready != nil {
		ready(info)
	}
	var runErr error
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = err
		}
	case <-ctx.Done():
	}
	logger.Printf("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if cs != nil {
		_ = cs.Shutdown(sctx)
	}
	if err := hs.Shutdown(sctx); err != nil && runErr == nil {
		runErr = fmt.Errorf("shutdown: %w", err)
	}
	return runErr
}

// displayAddr renders a bound address for a URL: an unspecified host (":8080")
// reads as 127.0.0.1 so the printed URL works on this machine.
func displayAddr(a net.Addr) string {
	ta, ok := a.(*net.TCPAddr)
	if !ok {
		return a.String()
	}
	host := ta.IP.String()
	if ta.IP.IsUnspecified() {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(ta.Port))
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// accessLog logs method, path (never the query or a header) and status.
func accessLog(l *log.Logger, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(sw, r)
		if r.URL.Path == "/healthz" {
			return
		}
		l.Printf("%q %q %d %s", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
	})
}
