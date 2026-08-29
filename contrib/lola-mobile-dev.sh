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
#   contrib/lola-mobile-dev.sh --info   print the connect details, run nothing
#   contrib/lola-mobile-dev.sh --key    print the bearer key only

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
home=${LOLA_HOME:-$HOME/.lola}
keyfile=$home/remote-dev-key
logfile=$home/daemon.log

die() { printf 'lola-mobile-dev: %s\n' "$1" >&2; exit 1; }

# The key lives outside the repository and is 0600, because it is a bearer
# credential: anything holding it can type into a live coding agent.
ensure_key() {
	[ -d "$home" ] || die "no $home — run lola once first"
	if [ ! -f "$keyfile" ]; then
		umask 077
		# 32 hex characters, comfortably over internal/remote's 16-char minimum.
		openssl rand -hex 16 > "$keyfile" || die "could not generate a key"
		printf 'generated a new bearer key at %s\n' "$keyfile" >&2
	fi
	chmod 600 "$keyfile" 2>/dev/null || true
	cat "$keyfile"
}

last_pin() {
	[ -f "$logfile" ] || return 0
	# The daemon logs "remote: phone listener up on <addrs> (SPKI pin <pin>)".
	sed -n 's/.*(SPKI pin \([^)]*\)).*/\1/p' "$logfile" | tail -1
}

case ${1:-} in
--key)
	ensure_key
	exit 0
	;;
--info)
	key=$(ensure_key)
	pin=$(last_pin)
	printf '\n  host  127.0.0.1        (Simulator only — see mobile/README.md section 4.5 for a device)\n'
	printf '  port  7717             (or [remote].port)\n'
	printf '  key   %s\n' "$key"
	if [ -n "$pin" ]; then
		printf '  pin   %s\n\n' "$pin"
	else
		printf '  pin   unknown — start the daemon once; it prints the pin on the listener line\n\n'
	fi
	exit 0
	;;
esac

# --- config -----------------------------------------------------------------
cfg=$home/config.toml
if [ ! -f "$cfg" ]; then
	die "no $cfg"
fi
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

# --- build ------------------------------------------------------------------
printf 'installing the lola_insecure build...\n' >&2
GOCACHE=$root/.gocache GOFLAGS='-mod=mod -buildvcs=false' \
	go install -tags lola_insecure "$root" || die "go install failed"

bin=$(command -v lola) || die "lola is not on PATH after go install — check \$GOPATH/bin is in PATH"
if ! go tool nm "$bin" 2>/dev/null | grep -q insecureAuthorizer; then
	die "$bin does not contain the insecure authorizer.
Something else on PATH is shadowing the build just installed."
fi
printf 'verified: %s carries the M1 listener\n' "$bin" >&2

key=$(ensure_key)

# --- run --------------------------------------------------------------------
# Stopping first is the point of the script: a daemon already running was almost
# certainly started without the key in its environment, and it would keep
# refusing every connection while looking perfectly healthy.
if lola status >/dev/null 2>&1; then
	printf 'stopping the running daemon (it does not hold the bearer key)...\n' >&2
	lola stop >/dev/null 2>&1 || true
fi

printf '\n  key   %s\n' "$key"
printf '  The listener line below carries the SPKI pin. Both go in the app.\n\n'

exec env LOLA_REMOTE_INSECURE_KEY="$key" lola run
