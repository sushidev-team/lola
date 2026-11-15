#!/usr/bin/env bash
# Opens a route, a sheet, or a fresh pairing on a booted Simulator. DEBUG builds only.
#
#   scripts/dev-link.sh --route /inbox?filter=needs
#   scripts/dev-link.sh --route /session/s1/conversation --sheet sessionMenu
#   scripts/dev-link.sh --pair [--route /inbox]
#
# Once paired, links carry only `route` and `sheet`: no credentials.
# `--pair` runs `lola pair --json`, hands its payload to the app through the
# launch environment (the Simulator has no camera), reads the 6-digit code the
# app logs (`lola-dev pair-code <code>`) and types it as the one stdin line
# `lola pair` waits for. Nothing is confirmed without that line.
#
# Environment: LOLA_SIM (a simulator UDID; default the booted one), LOLA_BIN
# (the lola binary for `--pair`; default `lola` on PATH). An E2E run against a
# throwaway daemon sets LOLA_BIN to that build and LOLA_HOME to its home.

set -euo pipefail

bundle=dev.sushi.lola.mobile
route=""
sheet=""
pair=0

die() { printf 'dev-link: %s\n' "$1" >&2; exit 1; }

while [ $# -gt 0 ]; do
	case $1 in
		--route) [ $# -ge 2 ] || die "--route needs a path"; route=$2; shift 2 ;;
		--sheet) [ $# -ge 2 ] || die "--sheet needs a name"; sheet=$2; shift 2 ;;
		--pair) pair=1; shift ;;
		*) die "usage: dev-link.sh [--route <path>] [--sheet <name>] [--pair]" ;;
	esac
done

udid=${LOLA_SIM:-}
if [ -z "$udid" ]; then
	udid=$(xcrun simctl list devices booted | sed -n 's/.*(\([0-9A-F-]\{36\}\)) (Booted).*/\1/p' | head -1)
	[ -n "$udid" ] || die "no booted simulator. Boot one, or set LOLA_SIM."
fi

# Percent-encodes one query value (POSIX tools only).
urlencode() {
	printf '%s' "$1" | od -An -tx1 -v | tr -d '\n' | tr -s ' ' '\n' | while read -r byte; do
		[ -n "$byte" ] || continue
		char=$(printf "\\x$byte")
		case $char in
			[a-zA-Z0-9._~-]) printf '%s' "$char" ;;
			*) printf '%%%s' "$byte" ;;
		esac
	done
}

query=""
add() { query="${query:+$query&}$1=$(urlencode "$2")"; }
[ -z "$route" ] || add route "$route"
[ -z "$sheet" ] || add sheet "$sheet"

launch() {
	xcrun simctl terminate "$udid" "$bundle" >/dev/null 2>&1 || true
	# LolaDevLink.parse accepts only `lola-dev://open?…`; a bare query is no link.
	SIMCTL_CHILD_LOLA_DEV_LINK="lola-dev://open?$1" xcrun simctl launch "$udid" "$bundle" >/dev/null
}

if [ "$pair" -eq 0 ]; then
	launch "$query"
	printf 'dev-link: route=%s sheet=%s\n' "${route:-none}" "${sheet:-none}" >&2
	exit 0
fi

work=$(mktemp -d)
logpid=""
pairpid=""
cleanup() {
	[ -z "$logpid" ] || kill "$logpid" 2>/dev/null || true
	[ -z "$pairpid" ] || kill "$pairpid" 2>/dev/null || true
	exec 3>&- 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup EXIT

mkfifo "$work/in"
# --allow-loopback: the Simulator shares the Mac's loopback, so a daemon bound
# to localhost (run-sim.sh's fastest loop) must be able to hand out a
# loopback-only QR. A LAN-bound daemon still lists its LAN addresses first.
"${LOLA_BIN:-lola}" pair --json --allow-loopback <"$work/in" >"$work/out" 2>"$work/err" &
pairpid=$!
exec 3>"$work/in"

wait_for() { # <pattern> <file>
	for _ in $(seq 1 600); do
		grep -q "$1" "$2" 2>/dev/null && return 0
		kill -0 "$pairpid" 2>/dev/null || return 1
		sleep 0.1
	done
	return 1
}

wait_for '"event":"begin"' "$work/out" || die "lola pair did not begin: $(cat "$work/err")"
payload=$(sed -n 's/.*"event":"begin".*"payload":"\([^"]*\)".*/\1/p' "$work/out" | head -1)
[ -n "$payload" ] || die "no payload in the begin line"

# --level info: LolaLog.devLinkNote logs at info, which `log stream` omits by default.
xcrun simctl spawn "$udid" log stream --level info --style compact \
	--predicate 'eventMessage BEGINSWITH "lola-dev pair-code"' >"$work/log" 2>/dev/null &
logpid=$!
sleep 1

add pair "$payload"
launch "$query"

wait_for '"state":"confirming"' "$work/out" || die "the app did not present the pairing: $(cat "$work/err")"
code=""
for _ in $(seq 1 100); do
	code=$(sed -n 's/.*lola-dev pair-code \([0-9]\{6\}\).*/\1/p' "$work/log" | tail -1)
	[ -z "$code" ] || break
	sleep 0.1
done
[ -n "$code" ] || die "no pair code in the device log"

printf '%s\n' "$code" >&3
wait "$pairpid" || die "pairing did not complete: $(cat "$work/err")"
printf 'dev-link: paired\n' >&2
