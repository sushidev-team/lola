#!/bin/sh
# Build a full-bleed, rounded-squircle darwin/icons.icns from
# darwin/appicon-rounded.png using only macOS built-ins (sips + iconutil).
#
# Why this exists: `wails3 generate icons` wraps opaque input in a "Big Sur
# tray", which macOS 26 (Liquid Glass) then re-rounds — producing a nested
# square-in-a-square in the Dock. We instead ship a pre-rounded, transparent-
# corner source so the icon is a single squircle that fills the tile.
#
# Regenerating the rounded source after an icon change (needs ImageMagick).
# appicon.svg is the canonical master (the full square icon with background):
#   rsvg-convert -w 1024 -h 1024 appicon.svg -o /tmp/appicon.png
#   magick -size 1024x1024 xc:none -fill white \
#     -draw "roundrectangle 0,0,1023,1023,224,224" /tmp/mask.png
#   magick /tmp/appicon.png /tmp/mask.png -alpha set -compose DstIn -composite \
#     darwin/appicon-rounded.png
#
# Run from the build/ dir (the generate:icons task sets dir: build).
set -e

SRC="darwin/appicon-rounded.png"
OUT="darwin/icons.icns"
[ -f "$SRC" ] || { echo "make-icns: $SRC missing; keeping existing $OUT" >&2; exit 0; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
SET="$TMP/icon.iconset"
mkdir -p "$SET"

# name  size
for pair in \
  "icon_16x16.png 16" \
  "icon_16x16@2x.png 32" \
  "icon_32x32.png 32" \
  "icon_32x32@2x.png 64" \
  "icon_128x128.png 128" \
  "icon_128x128@2x.png 256" \
  "icon_256x256.png 256" \
  "icon_256x256@2x.png 512" \
  "icon_512x512.png 512" \
  "icon_512x512@2x.png 1024"; do
  name="${pair% *}"; size="${pair##* }"
  sips -z "$size" "$size" "$SRC" --out "$SET/$name" >/dev/null
done

iconutil -c icns "$SET" -o "$OUT"
echo "make-icns: wrote full-bleed $OUT"
