#!/bin/sh
# Build the app and run it on a PHYSICAL iPhone, without opening Xcode.
#
# The Simulator loop (scripts/run-sim.sh) covers almost everything, but three
# things only exist on hardware and all three matter to this app:
#
#   - The CAMERA, and therefore the QR scanner. `scanCapability()` reports
#     `no_camera` on the Simulator and the connect screen correctly hides the
#     scan button, so that whole path is unexercised until a device runs it.
#   - The LOCAL NETWORK permission. iOS reports a denied one as an ordinary
#     unreachable host and never prompts twice, so it is invisible in the
#     Simulator (which shares the Mac's loopback and never triggers it).
#   - A real network. Suspension on backgrounding, a partition when you walk out
#     of range, and the keep-alive that is supposed to notice — none of which a
#     shared loopback can reproduce.
#
# Usage:
#   ./scripts/run-device.sh                 build, install and launch
#   ./scripts/run-device.sh --list          show attached devices and exit
#   LOLA_DEVICE=<udid> ./scripts/run-device.sh
#
# The first run needs three things Xcode owns and this script cannot do for you;
# it checks for each and says exactly what to do rather than failing in
# xcodebuild's own words. See mobile/README.md section 7.

# Safari's Web Inspector is enabled for these builds and only for these builds:
# capacitor.config.ts reads LOLA_WEB_INSPECTOR at sync time and it defaults off,
# so that a build carrying a durable Keychain credential does not ship with a
# debugger attached by default. Set LOLA_WEB_INSPECTOR=0 to build without it.

set -eu

export LOLA_WEB_INSPECTOR="${LOLA_WEB_INSPECTOR:-1}"


cd "$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"

BUNDLE_ID=dev.sushi.lola.mobile
PROJ=ios/App/App.xcodeproj

die() { printf 'run-device: %s\n' "$1" >&2; exit 1; }

command -v xcrun >/dev/null 2>&1 || die "Xcode command line tools not found."

# --- pick a device ---------------------------------------------------------
#
# devicectl is the current tool; the older `instruments -s devices` is gone.
# Only CONNECTED and paired devices are offered: an unavailable one produces a
# build that succeeds and an install that fails minutes later.
devices=$(xcrun devicectl list devices 2>/dev/null) || die "could not list devices"

if [ "${1:-}" = "--list" ]; then
	printf '%s\n' "$devices"
	exit 0
fi

if [ -n "${LOLA_DEVICE:-}" ]; then
	udid=$LOLA_DEVICE
	name=$(printf '%s\n' "$devices" | awk -F'[[:space:]][[:space:]]+' -v u="$udid" '$3 == u { print $1; exit }')
else
	# devicectl prints a fixed-width table whose columns are separated by runs of
	# two or more spaces: Name, Hostname, Identifier, State, Model. Split on the
	# GUTTERS rather than on whitespace — several columns contain single spaces
	# of their own ("Apple Bobsch", "iPhone 13 Pro (iPhone14,2)"), so counting
	# fields finds the wrong column and silently reports no device.
	#
	# A paired Watch appears here too and cannot run this app, so it is excluded
	# by model rather than by name: the name is whatever its owner chose.
	row=$(printf '%s\n' "$devices" \
		| awk -F'[[:space:]][[:space:]]+' '$4 == "connected" && $5 !~ /^Watch/ { print; exit }')
	[ -n "$row" ] || die "no connected iPhone.

Connect it by cable, unlock it, and tap Trust if asked. Then, once per phone:
  Settings > Privacy & Security > Developer Mode > on (the phone restarts)

Run './scripts/run-device.sh --list' to see what Xcode can currently see."
	udid=$(printf '%s\n' "$row" | awk -F'[[:space:]][[:space:]]+' '{print $3}')
	name=$(printf '%s\n' "$row" | awk -F'[[:space:]][[:space:]]+' '{print $1}')
fi

[ -n "$udid" ] || die "could not determine a device identifier; try LOLA_DEVICE=<udid>"

# --- signing ---------------------------------------------------------------
#
# A device build must be signed; a Simulator build need not be, which is why
# this has not come up before. The team is per-developer, so it is kept OUT of
# the repository: ios/debug.xcconfig is tracked, and a Team ID committed there
# would be published with the project. It is read from ios/team.local (which
# .gitignore excludes) or from LOLA_TEAM.
team=${LOLA_TEAM:-}
if [ -z "$team" ] && [ -f ios/team.local ]; then
	team=$(tr -d ' \t\r\n' < ios/team.local)
fi
if [ -z "$team" ]; then
	# Discover it instead of asking. An "Apple Development" identity in the
	# keychain is by definition an iOS development certificate, and the
	# parenthesised value in its name is the Team ID. "Developer ID Application"
	# identities are deliberately NOT offered: those sign Mac software for
	# distribution outside the App Store (this repo uses one to notarize the
	# desktop app) and cannot sign an iOS device build.
	teams=$(security find-identity -v -p codesigning 2>/dev/null \
		| sed -n 's/.*"Apple Development: .*(\([A-Z0-9]\{10\}\))".*/\1/p' \
		| sort -u)
	count=$(printf '%s\n' "$teams" | grep -c . || true)
	if [ "$count" = "1" ]; then
		team=$teams
		printf 'run-device: using the only Apple Development team on this Mac (%s).\n' "$team" >&2
		printf "run-device: save it with: echo %s > ios/team.local\n" "$team" >&2
	fi
fi
if [ -z "$team" ]; then
	die "no signing team.

A device build has to be signed and the team is personal to you, so it is not
committed. If this Mac has more than one Apple Development team, pick the one
whose account has this phone registered:

  security find-identity -v -p codesigning | grep 'Apple Development'

Then save it (this file is gitignored):

  echo ABCDE12345 > ios/team.local

or pass it for one run:

  LOLA_TEAM=ABCDE12345 ./scripts/run-device.sh

A free Apple ID works. Its provisioning expires after 7 days, so the app stops
launching after a week and you rebuild — that is Apple's limit, not this
script's."
fi

printf 'run-device: %s (%s), team %s\n' "${name:-device}" "$udid" "$team" >&2

# --- build the web assets --------------------------------------------------
# Same reason run-sim.sh does it: `cap sync` COPIES dist/, it does not build it,
# so skipping this ships whatever was in dist/ from the last build.
if [ "${LOLA_SKIP_BUILD:-}" != "1" ]; then
	npm run build:plugin
	npm run build
fi
npx cap sync ios

# --- build, install, launch ------------------------------------------------
out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT

xcodebuild \
	-project "$PROJ" \
	-scheme App \
	-configuration Debug \
	-destination "id=$udid" \
	-derivedDataPath "$out" \
	DEVELOPMENT_TEAM="$team" \
	CODE_SIGN_STYLE=Automatic \
	-allowProvisioningUpdates \
	build || die "xcodebuild failed.

If it is a provisioning error, open the project in Xcode once and let it repair:
  npx cap open ios
Xcode can create a development certificate and register this device; the command
line cannot do that part the first time."

app=$(find "$out/Build/Products" -maxdepth 2 -name 'App.app' -type d | head -1)
[ -n "$app" ] || die "built, but no App.app was produced"

xcrun devicectl device install app --device "$udid" "$app" >/dev/null \
	|| die "install failed. Is the phone unlocked?"

xcrun devicectl device process launch --device "$udid" "$BUNDLE_ID" >/dev/null \
	|| die "installed, but could not launch. Launch it from the home screen.

On the FIRST launch of a build signed with a free Apple ID, iOS refuses until
you trust the certificate:
  Settings > General > VPN & Device Management > (your Apple ID) > Trust"

printf 'run-device: launched on %s\n' "${name:-device}" >&2
printf '\nNext: the phone needs to reach the daemon, and a lola_insecure build binds\n' >&2
printf 'to loopback — which a physical phone cannot reach. See mobile/README.md\n' >&2
printf 'section 7 for the options.\n' >&2
