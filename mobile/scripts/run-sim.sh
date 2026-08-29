#!/bin/sh
# Build the app and launch it on an iOS Simulator, without opening Xcode.
#
# `cap run ios` does the whole sequence — sync, xcodebuild, install, launch —
# but it needs a target UDID, and it prompts interactively when none is given.
# This picks one so the command is non-interactive: the NEWEST iOS runtime among
# the installed iPhone simulators, which is almost always what you want and is
# never an iPad.
#
# Override with LOLA_SIM, either a UDID or a substring of the simulator's name:
#
#   LOLA_SIM="iPhone 17 Pro" npm run sim
#   LOLA_SIM=F88E2DEB-0C32-4F02-B5C8-D205D6BBF49E npm run sim
#
# Pass -l for live reload, which serves the web assets from the vite dev server
# instead of the copy inside the bundle, so a Svelte change reloads without a
# rebuild. The native plugin still requires a rebuild:
#
#   npm run sim -- -l
#
# Note that a Simulator shares the Mac's loopback, so 127.0.0.1 in the connect
# screen reaches a daemon bound to localhost with no forwarding at all. That is
# why this is the first thing to try, and why it is the fastest loop.

set -eu

cd "$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"

targets=$(npx cap run ios --list 2>/dev/null) || {
	echo "run-sim: could not list simulators. Is Xcode installed and are its command line tools selected?" >&2
	exit 1
}

if [ -n "${LOLA_SIM:-}" ]; then
	# A UDID matches the last column; a name matches anywhere in the row.
	row=$(printf '%s\n' "$targets" | grep -i -- "$LOLA_SIM" | grep '(simulator)' | head -1) || true
	[ -n "$row" ] || { echo "run-sim: no simulator matches LOLA_SIM=$LOLA_SIM" >&2; exit 1; }
	udid=$(printf '%s\n' "$row" | awk '{print $NF}')
	name=$(printf '%s\n' "$row" | sed 's/  */ /g' | sed 's/ (simulator).*//')
else
	# Newest runtime among iPhone simulators. sort -V orders "26.5" above "9.0"
	# and above "15.5", which a lexical sort does not.
	row=$(printf '%s\n' "$targets" \
		| grep '(simulator)' \
		| grep '^iPhone' \
		| awk '{print $(NF-1)"\t"$0}' \
		| sort -V \
		| tail -1 \
		| cut -f2-)
	[ -n "$row" ] || { echo "run-sim: no iPhone simulator installed. Xcode > Settings > Components." >&2; exit 1; }
	udid=$(printf '%s\n' "$row" | awk '{print $NF}')
	name=$(printf '%s\n' "$row" | sed 's/  */ /g' | sed 's/ (simulator).*//')
fi

printf 'run-sim: %s (%s)\n' "$name" "$udid" >&2

# BUILD FIRST. `cap run ios` runs `cap sync`, and sync COPIES dist/ — it does
# not produce it. So without this the command cheerfully deploys whatever was
# in dist/ the last time somebody built, which is the worst possible failure
# for this script: it succeeds, the app launches, and the change under test is
# simply not in it. That cost a debugging session already.
#
# build:dev rather than build: this is the dev loop, and an unminified bundle
# is what makes a Safari Web Inspector trace readable. The plugin comes first
# because dist/ imports its types.
#
# LOLA_SKIP_BUILD=1 skips both, for the case where the web assets are known
# fresh and only the native side is being re-deployed.
if [ -z "${LOLA_SKIP_BUILD:-}" ]; then
	printf 'run-sim: building the plugin and the web assets...\n' >&2
	npm run build:plugin >&2 || { echo "run-sim: plugin build failed" >&2; exit 1; }
	npm run build:dev >&2 || { echo "run-sim: web build failed" >&2; exit 1; }
fi

exec npx cap run ios --target "$udid" "$@"
