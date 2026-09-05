#!/bin/sh
# Bring up a daemon a phone can actually connect to, for milestone 1 of the
# mobile app (see mobile/PLAN.md and mobile/README.md).
#
# This exists because the M1 start-up sequence has four steps that are easy to
# get wrong and that fail in ways which look like an application bug:
#
#   1. The listener only exists in a build tagged `lola_insecure`, and
#      `make build` does not pass that tag.
#   2. The binary that actually runs is resolved from PATH, not from the repo,
#      so building without installing restarts the daemon onto the old code.
#   3. The bearer key is read from the daemon's own environment, so a daemon
#      started by launchd — or re-execed by the TUI from a shell that never
#      exported it — refuses every connection with no useful signal.
#   4. A tagged build forces the bind to loopback whatever [remote].bind says,
#      so a phone on the same network reaches nothing and reports an ordinary
#      timeout.
#
# It deliberately does NOT edit ~/.lola/config.toml. A script that rewrites an
# operator's configuration to make its own happy path work is how a machine ends
# up listening for reasons nobody remembers; this one reports what is wrong and
# lets a human decide.
#
# Usage:
#   contrib/lola-mobile-dev.sh          install the tagged build, then run it
#   contrib/lola-mobile-dev.sh --lan    the same, but reachable from a real phone
#   contrib/lola-mobile-dev.sh --head   build from committed HEAD, not the working tree
#   contrib/lola-mobile-dev.sh --build-only
#                                       install the build and stop there, running nothing
#   contrib/lola-mobile-dev.sh --info   print the connect details, run nothing
#   contrib/lola-mobile-dev.sh --key    print the bearer key only

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
home=${LOLA_HOME:-$HOME/.lola}
# The DAEMON owns this file: internal/remote generates it on first start, 0600,
# and reuses it afterwards. This script only ever READS it, so the key it prints
# is the one the listener is actually accepting. An earlier version kept its own
# copy, which drifted the moment the daemon generated one and printed a key the
# phone would be refused with.
keyfile=$home/remote.key
logfile=$home/daemon.log
lan=
head=
build_only=

die() { printf 'lola-mobile-dev: %s\n' "$1" >&2; exit 1; }

# REFUSE TO PRINT THE KEY INTO A PANE LOLA READS.
#
# mobile/PLAN.md requires this of `lola pair` and gives the reason; it applies
# to every command that prints the bearer key, which is these two. lola captures
# its own tmux panes and ships the capture onward: internal/daemon's
# `[statusagent]` hands a pane tail to a bounded `claude -p` whose instruction
# opens "Standard input contains observed material about one session: a terminal
# pane capture", and `[brain]` does the same with a 40-line tail. Both are
# enabled in ordinary configurations. So an agent that runs `make mobile-info`
# in its own session sends a credential that types into live coding agents to a
# third-party model — and unlike an ordinary secret this one is not rotated,
# not scoped and not revocable per device.
#
# The way out is the affordance this milestone added: the desktop app's
# Settings -> Remote draws the same four values from the running listener, on a
# surface with no pane to capture.
refuse_in_session() {
	[ -z "${LOLA_SESSION:-}" ] || die "refusing to print the bearer key inside a lola session.

\$LOLA_SESSION is set, so this pane is captured by lola and its text is sent to
the [brain] summarizer and the [statusagent] interpreter — a third-party model.
The key types into live coding agents and cannot be revoked per device.

Read it in the desktop app instead: Settings -> Remote -> Show code.
Or run this from a terminal outside any lola session."
}

# Reading, never writing. A missing file means no daemon has started under this
# home yet, which is worth reporting rather than papering over by inventing a
# key the listener would reject.
read_key() {
	if [ -f "$keyfile" ]; then
		tr -d ' \t\r\n' < "$keyfile"
	fi
}

last_pin() {
	[ -f "$logfile" ] || return 0
	# The daemon logs "remote: phone listener up on <addrs> (SPKI pin <pin>)".
	sed -n 's/.*(SPKI pin \([^)]*\)).*/\1/p' "$logfile" | tail -1
}

# Flags COMBINE, which is why this is a loop rather than a single-argument
# case: `--lan --head` is the ordinary way to refresh an operator's daemon.
while [ $# -gt 0 ]; do
	case $1 in
	--key)
		refuse_in_session
		read_key
		exit 0
		;;
	--info)
		refuse_in_session
		key=$(read_key)
		pin=$(last_pin)
		# The addresses the listener actually bound, rather than a guess: with the
		# LAN opt-in off it is loopback and only a Simulator can reach it, and with
		# it on the phone needs whichever private address the daemon chose.
		addrs=$(sed -n 's/.*phone listener up on \(.*\) (SPKI pin.*/\1/p' "$logfile" 2>/dev/null | tail -1)
		# A WILDCARD bind ("all") reports [::]:7717, which is not something anyone can
		# type into a phone. Resolve it to an address that is actually reachable —
		# loopback if it only bound loopback, otherwise this machine's private IPv4.
		# This is also why "lan" is the better setting: it binds each private
		# interface by name, so the log already says where to point the phone.
		host=127.0.0.1
		case $addrs in
		*"[::]"* | *0.0.0.0*)
			for i in en0 en1 en2; do
				ip=$(ipconfig getifaddr "$i" 2>/dev/null) && [ -n "$ip" ] && host=$ip && break
			done
			;;
		"") ;;
		*)
			# Take the first address and strip its port, keeping IPv6 brackets intact.
			first=${addrs%%,*}
			host=$(printf '%s' "$first" | sed 's/:[0-9]*$//')
			;;
		esac
		printf '\n'
		if [ -n "$addrs" ]; then
			printf '  bound %s\n' "$addrs"
		fi
		printf '  host  %s\n' "$host"
		printf '  port  7717             (or [remote].port)\n'
		if [ -n "$key" ]; then
			printf '  key   %s        (a bearer credential — see mobile/README.md)\n' "$key"
		else
			printf '  key   none yet — the daemon generates one at %s on first start\n' "$keyfile"
		fi
		if [ -n "$pin" ]; then
			printf '  pin   %s\n\n' "$pin"
		else
			printf '  pin   unknown — start the daemon once; it prints the pin on the listener line\n\n'
		fi
		exit 0
		;;
	--lan)
		lan=1
		;;
	--head)
		head=1
		;;
	--build-only)
		build_only=1
		;;
	*)
		die "unknown option $1 (--lan, --head, --build-only, --info, --key)"
		;;
	esac
	shift
done

# --- config -----------------------------------------------------------------
#
# Every check here is about the daemon this script is about to RUN — whether it
# will listen, and on what. --build-only runs nothing, so it must not fail over
# a configuration it is not going to use: installing a binary is useful on a
# machine that has not set up [remote] at all.
cfg=$home/config.toml
if [ -n "$build_only" ]; then
	:
elif [ ! -f "$cfg" ]; then
	die "no $cfg"
fi
if [ -z "$build_only" ]; then
if ! grep -q '^[[:space:]]*\[remote\]' "$cfg"; then
	die "no [remote] table in $cfg — add:

    [remote]
    enabled = true
    bind = \"localhost\"
    port = 7717
"
fi
if ! sed -n '/^[[:space:]]*\[remote\]/,/^[[:space:]]*\[[^r]/p' "$cfg" | grep -q '^[[:space:]]*enabled[[:space:]]*=[[:space:]]*true'; then
	die "[remote] is present in $cfg but not enabled = true"
fi

fi

# --- build ------------------------------------------------------------------
#
# --head BUILDS THE COMMITTED TREE, not the working one, and that is the whole
# reason it exists. This repository is routinely edited by more than one agent
# at a time, so the working tree can hold somebody else's half-finished
# refactor — and the daemon this script installs is the operator's real one.
# `git archive HEAD` cannot pick up an uncommitted or untracked file, so what
# lands in $GOBIN is exactly what is committed.
#
# The output is written to a temp file and MOVED into place rather than built
# straight over the target: a rename is atomic and leaves any still-running
# daemon holding its old inode, which is the property the rest of this repo's
# tooling already relies on.
if [ -n "$head" ]; then
	printf 'installing the lola_insecure build from committed HEAD...\n' >&2
	command -v git >/dev/null 2>&1 || die "--head needs git"
	gobin=$(go env GOBIN)
	[ -n "$gobin" ] || gobin=$(go env GOPATH)/bin
	[ -d "$gobin" ] || die "no Go bin directory at $gobin"
	src=$(mktemp -d) || die "could not make a temp directory"
	git -C "$root" archive HEAD | tar -x -C "$src" ||
		{ rm -rf "$src"; die "could not export HEAD"; }
	GOCACHE=$root/.gocache GOFLAGS='-mod=mod -buildvcs=false' \
		go build -C "$src" -tags lola_insecure -o "$src/lola" . ||
		{ rm -rf "$src"; die "committed HEAD does not build.
The working tree may compile where HEAD does not — a commit that swept in a
file another agent had not finished will do exactly this. Fix the commit, or
run this script without --head to install the working tree instead."; }
	mv "$src/lola" "$gobin/lola" || { rm -rf "$src"; die "could not install into $gobin"; }
	rm -rf "$src"
else
	printf 'installing the lola_insecure build...\n' >&2
	GOCACHE=$root/.gocache GOFLAGS='-mod=mod -buildvcs=false' \
		go install -tags lola_insecure "$root" || die "go install failed"
fi

bin=$(command -v lola) || die "lola is not on PATH after go install — check \$GOPATH/bin is in PATH"
if ! go tool nm "$bin" 2>/dev/null | grep -q insecureAuthorizer; then
	die "$bin does not contain the insecure authorizer.
Something else on PATH is shadowing the build just installed."
fi
printf 'verified: %s carries the M1 listener\n' "$bin" >&2

# --- build only ---------------------------------------------------------------
#
# Stops here on purpose, and says what is still true: the daemon that is running
# right now is the one that was already running. Replacing the FILE does not
# change a running process — it keeps the inode it started with — so a build
# without a restart is a no-op from the outside, which is exactly the trap that
# costs a debugging session (see CLAUDE.md, "make build alone never reaches the
# running daemon").
if [ -n "$build_only" ]; then
	printf '\n  Installed, and NOT started.\n' >&2
	if lola status >/dev/null 2>&1; then
		printf '  A daemon is still running on the OLD binary — a running process keeps\n' >&2
		printf '  the inode it started with, so this build is not live until it restarts.\n' >&2
	fi
	printf '  Start it with: make daemon-dev\n\n' >&2
	exit 0
fi

# --- run --------------------------------------------------------------------
# Stopping first is the point of the script. A daemon already running was very
# likely started by the TUI's ^r or the desktop app's restart button, neither of
# which passes the LAN opt-in — so it is bound to loopback and a phone cannot
# reach it, however healthy it looks.
if lola status >/dev/null 2>&1; then
	printf 'stopping the running daemon...\n' >&2
	lola stop >/dev/null 2>&1 || true
fi

# The key is NOT passed in the environment any more: the daemon generates and
# reuses its own, which is what makes it survive a restart this script did not
# perform. Anyone who wants to supply their own still exports
# LOLA_REMOTE_INSECURE_KEY themselves, and internal/remote prefers it.
if [ -n "$lan" ]; then
	printf '\n  Binding beyond loopback, so a physical phone can reach this daemon.\n' >&2
	printf '  The bearer key crosses your network in the clear. Use a network you control.\n' >&2
	printf '  [remote].bind decides WHICH interfaces; "lan" is narrower than "all".\n\n' >&2
	exec env LOLA_REMOTE_INSECURE_LAN=1 lola run
fi

printf '\n  Bound to loopback, which a Simulator shares and a physical phone cannot\n' >&2
printf '  reach. Use --lan for a real device.\n' >&2
printf '  Connect details: make mobile-info, or the desktop app under Settings > Remote.\n\n' >&2

exec lola run
