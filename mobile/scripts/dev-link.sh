#!/bin/sh
# Put the app in front of the running daemon on a Simulator, without a camera
# and WITHOUT writing the bearer key into the device's log.
#
# WHY THIS SCRIPT EXISTS RATHER THAN A LINE IN THE README.
#
# The Simulator has no camera, so the QR hand-off — the connect screen's primary
# action — is exactly the path that cannot be exercised on the one device a
# script can drive. `lola-dev://connect?...` is the answer to that, and the
# obvious spelling of it discloses the credential: iOS logs the URL for every
# delivery route it has. `simctl openurl` produces
#
#   CoreSimulatorBridge: Opening URL (lola-dev://connect?...&key=<the key>)
#
# in the device's persistent unified log; `SIMCTL_CHILD_LOLA_DEV_LINK` is logged
# as part of the child's environment dictionary; `--lola-dev-link` is logged
# twice as part of the argument vector. That store survives a relaunch, is what
# `simctl diagnose` collects, and the credential in it types into live coding
# agents. The plugin cannot fix this — the logging happens in the OS before the
# app runs.
#
# So the URL carries a FILE NAME instead. A name is not a credential; the bytes
# are staged into the app's own container, read once and deleted. See
# `LolaDevLink.readKeyFile`.
#
# The key is never printed by this script either, which matters when it is run
# from inside a lola session: lola captures its own panes and ships the capture
# to the [brain] summarizer and the [statusagent] interpreter.
#
# Usage:
#   scripts/dev-link.sh                      connect, land on the session list
#   scripts/dev-link.sh lola-nori-eng-42     connect and open that pane
#
# Environment:
#   LOLA_SIM     a simulator UDID. Defaults to the booted one.
#   LOLA_HOST    the address the app dials. Defaults to 127.0.0.1, which the
#                Simulator shares with the Mac.
#   LOLA_PORT    defaults to 7717.

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
home=${LOLA_HOME:-$HOME/.lola}
bundle=dev.sushi.lola.mobile
host=${LOLA_HOST:-127.0.0.1}
port=${LOLA_PORT:-7717}
pane=${1:-}

die() { printf 'dev-link: %s\n' "$1" >&2; exit 1; }

udid=${LOLA_SIM:-}
if [ -z "$udid" ]; then
	udid=$(xcrun simctl list devices booted | sed -n 's/.*(\([0-9A-F-]\{36\}\)) (Booted).*/\1/p' | head -1)
	[ -n "$udid" ] || die "no booted simulator. Boot one, or set LOLA_SIM."
fi

# Read, never echo.
# TWO NAMES, and the second one is now the usual case. This script predates the
# daemon generating its own bearer key: back then `make mobile-dev` exported one
# and wrote it to `remote-dev-key`, and that is still honoured for anyone who
# has one. The daemon now generates and reuses `remote.key` instead — which is
# what makes the key survive a restart this script did not perform — so a clean
# machine has only that file, and the old path died telling the reader to run a
# command that no longer produces what it names.
keyfile=$home/remote-dev-key
[ -f "$keyfile" ] || keyfile=$home/remote.key
[ -f "$keyfile" ] || die "no $home/remote.key — start the daemon once, or run 'make mobile-dev'"
key=$(cat "$keyfile")
[ -n "$key" ] || die "$keyfile is empty"

# The pin is public (a hash of a public key) and is the one value that is safe
# in the URL. It is read from the listener line the daemon logged.
pin=$(sed -n 's/.*(SPKI pin \([^)]*\)).*/\1/p' "$home/daemon.log" 2>/dev/null | tail -1)
[ -n "$pin" ] || die "no SPKI pin in $home/daemon.log — start the daemon once"

container=$(xcrun simctl get_app_container "$udid" "$bundle" data 2>/dev/null) \
	|| die "$bundle is not installed on $udid — run ./scripts/run-sim.sh first"
docs=$container/Documents
mkdir -p "$docs"

# 0600 and a fixed name. The app deletes it after reading, so a run that never
# reaches the app is the only way one survives; `simctl diagnose` would collect
# it, which is why it is not left lying around on purpose.
staged=lola-dev-key
umask 077
printf '%s' "$key" > "$docs/$staged"

link="lola-dev://connect?host=$host&port=$port&pin=$pin&keyfile=$staged"
[ -z "$pane" ] || link="$link&pane=$pane"

printf 'dev-link: %s:%s pane=%s (key staged in the app container, not in the URL)\n' \
	"$host" "$port" "${pane:-none}" >&2

xcrun simctl terminate "$udid" "$bundle" >/dev/null 2>&1 || true
SIMCTL_CHILD_LOLA_DEV_LINK="$link" exec xcrun simctl launch "$udid" "$bundle"
