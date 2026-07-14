package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"time"

	"github.com/sushidev-team/lola/internal/ao"
	"github.com/sushidev-team/lola/internal/config"
	"github.com/sushidev-team/lola/internal/protocol"
)

// serve runs the accept loop until the listener is closed at shutdown.
func (d *Daemon) serve(ctx context.Context, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return
			}
			d.logf("", "accept: %v", err)
			continue
		}
		go d.handleConn(ctx, conn)
	}
}

// handleConn reads newline-delimited protocol.Requests and answers one JSON
// line per request.
func (d *Daemon) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	enc := json.NewEncoder(conn)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req protocol.Request
		if err := json.Unmarshal(line, &req); err != nil {
			if enc.Encode(protocol.Response{OK: false, Error: "bad request: " + err.Error()}) != nil {
				return
			}
			continue
		}
		resp := d.handle(ctx, req)
		if enc.Encode(resp) != nil {
			return
		}
		if req.Cmd == "stop" && resp.OK {
			d.cancel() // reply is on the wire; now begin graceful shutdown
			return
		}
	}
}

func (d *Daemon) handle(ctx context.Context, req protocol.Request) protocol.Response {
	switch req.Cmd {
	case "stop":
		d.logf("", "stop requested via socket")
		return protocol.Response{OK: true}
	case "status":
		return dataResponse(d.statusData(ctx))
	case "sessions":
		return dataResponse(d.sessionsData())
	case "reload":
		if err := d.handleReload(ctx); err != nil {
			return protocol.Response{OK: false, Error: err.Error()}
		}
		return protocol.Response{OK: true}
	case "enable", "disable":
		if err := d.handleEnable(ctx, req.Poll, req.Cmd == "enable"); err != nil {
			return protocol.Response{OK: false, Error: err.Error()}
		}
		return protocol.Response{OK: true}
	case "pollOnce":
		data, err := d.handlePollOnce(ctx, req.Poll, req.DryRun)
		if err != nil {
			return protocol.Response{OK: false, Error: err.Error()}
		}
		return dataResponse(data)
	default:
		return protocol.Response{OK: false, Error: fmt.Sprintf("unknown cmd %q", req.Cmd)}
	}
}

// sessionsData builds the reply for cmd=sessions from the observer's cached
// store snapshot. Nothing is exec'd on the request path — a stale-but-instant
// answer beats a request that hangs on ao/gh/tmux (observer cadence is 30s).
func (d *Daemon) sessionsData() protocol.SessionsData {
	snap := d.sessions.Snapshot()
	now := time.Now()
	out := protocol.SessionsData{Sessions: make([]protocol.SessionInfo, 0, len(snap))}
	for _, s := range snap {
		si := protocol.SessionInfo{
			ID:       s.ID,
			Project:  s.Project,
			Issue:    s.Issue,
			Branch:   s.Branch,
			Status:   s.Status,
			TmuxName: s.TmuxName,
			Age:      formatAge(now.Sub(s.FirstSeen)),
		}
		if s.PR != nil {
			si.PRURL = s.PR.URL
			si.PRNumber = s.PR.Number
			si.Checks = s.PR.ChecksState
			si.Review = s.PR.ReviewDecision
		}
		out.Sessions = append(out.Sessions, si)
	}
	return out
}

// formatAge renders a duration TUI-compactly: "42s", "12m", "3h05m", "2d14h".
func formatAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		days := int(d.Hours()) / 24
		return fmt.Sprintf("%dd%dh", days, int(d.Hours())%24)
	}
}

func dataResponse(v any) protocol.Response {
	b, err := json.Marshal(v)
	if err != nil {
		return protocol.Response{OK: false, Error: err.Error()}
	}
	return protocol.Response{OK: true, Data: b}
}

// handleReload re-reads config.DefaultPath and applies it live. An invalid
// config is rejected: the old one keeps running.
func (d *Daemon) handleReload(ctx context.Context) error {
	path, err := config.DefaultPath()
	if err != nil {
		return err
	}
	nc, err := config.Load(path)
	if err != nil {
		return err
	}
	if err := nc.Validate(); err != nil {
		return fmt.Errorf("config invalid, keeping previous: %w", err)
	}

	d.mu.Lock()
	old := d.cfg
	d.cfg = nc
	d.cfgErr = "" // a validated config lifts the startup hold on polls
	if old.Linear != nc.Linear {
		d.lin = nil // key source / endpoint changed: re-resolve lazily
		d.viewerID = ""
	}
	if d.realAO && old.AO.Bin != nc.AO.Bin {
		d.aoc = &ao.Client{Bin: nc.AO.Bin}
	}
	d.mu.Unlock()

	d.syncWorkers(ctx)
	d.logf("", "config reloaded")
	return nil
}

// handleEnable flips a poll's Enabled flag, validates (incl. the ao_project
// existence check on enable), saves the config, and applies it live.
func (d *Daemon) handleEnable(ctx context.Context, name string, enable bool) error {
	if name == "" {
		return errors.New("poll name required")
	}

	// The ao_project check execs the ao binary; run it BEFORE taking d.mu for
	// the flip. Holding the daemon lock across an exec would freeze every
	// tick, status, reload, and reconcile if the ao binary wedges.
	var checkedProject string
	if enable {
		d.mu.Lock()
		p := d.cfg.PollByName(name)
		if p == nil {
			d.mu.Unlock()
			return fmt.Errorf("unknown poll %q", name)
		}
		checkedProject = p.AOProject
		aoc, aoConfigPath := d.aoc, d.cfg.AO.ConfigPath
		d.mu.Unlock()
		if err := checkAOProject(ctx, aoc, aoConfigPath, name, checkedProject); err != nil {
			return err
		}
	}

	d.mu.Lock()
	p := d.cfg.PollByName(name)
	if p == nil {
		d.mu.Unlock()
		return fmt.Errorf("unknown poll %q", name)
	}
	if enable && p.AOProject != checkedProject {
		// A concurrent reload swapped the config between the unlocked AO
		// check and now; don't enable a poll whose project was never checked.
		d.mu.Unlock()
		return fmt.Errorf("poll %q changed while validating ao_project; retry", name)
	}
	prev := p.Enabled
	p.Enabled = enable

	fail := func(err error) error {
		p.Enabled = prev
		d.mu.Unlock()
		return err
	}
	if err := d.cfg.Validate(); err != nil {
		return fail(err)
	}
	path, err := config.DefaultPath()
	if err == nil {
		err = d.cfg.Save(path)
	}
	if err != nil {
		return fail(err)
	}
	d.mu.Unlock()

	d.syncWorkers(ctx)
	verb := "disabled"
	if enable {
		verb = "enabled"
	}
	d.logf(name, "poll %s", verb)
	return nil
}

// aoProjectCheckTimeout bounds the `ao project ls --json` exec during enable,
// matching the TUI's loadAOProjects. A var so tests can shrink it.
var aoProjectCheckTimeout = 5 * time.Second

// checkAOProject verifies the poll's ao_project is registered in AO. The
// authoritative source is the live registry (`ao project ls --json`) —
// desktop AO builds keep it in SQLite, not a yaml file. When AO is down, the
// exec times out, or the registry lists nothing (fresh install), it falls
// back to scanning aoConfigPath; with neither available only the non-empty
// requirement applies. Must be called WITHOUT d.mu held: it execs the ao
// binary, which can block.
func checkAOProject(ctx context.Context, aoc AOAPI, aoConfigPath, pollName, aoProject string) error {
	if aoProject == "" {
		return fmt.Errorf("poll %q: ao_project is required to enable", pollName)
	}
	cctx, cancel := context.WithTimeout(ctx, aoProjectCheckTimeout)
	defer cancel()
	if ids, err := aoc.Projects(cctx); err == nil && len(ids) > 0 {
		if !slices.Contains(ids, aoProject) {
			return fmt.Errorf("poll %q: ao_project %q is not registered in AO (see `ao project ls`)", pollName, aoProject)
		}
		return nil
	}
	if aoConfigPath == "" {
		return nil
	}
	projects, err := config.AOProjects(aoConfigPath)
	if err != nil {
		return fmt.Errorf("read ao projects: %w", err)
	}
	if !slices.Contains(projects, aoProject) {
		return fmt.Errorf("poll %q: ao_project %q not found in %s", pollName, aoProject, aoConfigPath)
	}
	return nil
}

// handlePollOnce runs one tick now, mutually exclusive with the poll's
// ticker (a tick never runs twice concurrently for one poll). dryRun
// evaluates with zero side effects.
func (d *Daemon) handlePollOnce(ctx context.Context, name string, dryRun bool) (protocol.PollOnceData, error) {
	if name == "" {
		return protocol.PollOnceData{}, errors.New("poll name required")
	}
	d.mu.Lock()
	cfgErr := d.cfgErr
	d.mu.Unlock()
	if cfgErr != "" {
		return protocol.PollOnceData{}, errors.New(cfgErr + " (fix config.toml and run `lola reload`)")
	}
	// Register with the drain group so graceful shutdown waits for this
	// tick, and shield it from the shutdown cancellation like worker ticks.
	if !d.beginConnWork() {
		return protocol.PollOnceData{}, errors.New("daemon is shutting down")
	}
	defer d.connWg.Done()
	mu := d.tickMutex(name)
	mu.Lock()
	defer mu.Unlock()
	return d.tick(context.WithoutCancel(ctx), name, dryRun)
}
