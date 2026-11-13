#!/bin/sh
# Generate the iOS app icon and launch image from the repository's master art.
#
# Both asset catalogs shipped by `npx cap add ios` carry Capacitor's stock
# placeholders, and `cap sync` does NOT replace them — they are ours to fill.
# This regenerates both from the same sources the desktop app uses, so the two
# apps cannot drift apart:
#
#   desktop/build/appicon.png                          the 1024 full-bleed tile
#   desktop/frontend/src/lib/assets/lola-logo.svg      the wordmark
#
# Run it after changing either. It writes into mobile/ios/App/App/Assets.xcassets,
# which IS committed (see mobile/.gitignore's note on the generated project).
#
# Two iOS rules drive the choices below and are easy to get wrong:
#
#   - An app icon must be OPAQUE and SQUARE. iOS applies its own corner mask, so
#     pre-rounded art (desktop/build/darwin/appicon-rounded.png, which macOS
#     needs) is the wrong source here — it would be rounded twice, leaving pale
#     corners. Alpha is rejected outright at submission, which is why the source
#     is checked rather than assumed.
#   - The launch image is drawn scaleAspectFill on a square canvas, so a phone
#     crops the left and right. Anything that must survive belongs in the middle
#     third; the wordmark is sized to that, not to the canvas.

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
icon_src=$root/desktop/build/appicon.png
logo_src=$root/desktop/frontend/src/lib/assets/lola-logo.svg
assets=$root/mobile/ios/App/App/Assets.xcassets

# The brand ground. It is the rect at the top of desktop/build/appicon.svg, and
# it is repeated in LaunchScreen.storyboard so the launch image and the view
# behind it are the same colour — a mismatch shows as a flash on the seam.
BRAND_BG='#22242F'

[ -f "$icon_src" ] || { echo "make-ios-assets: missing $icon_src" >&2; exit 1; }
[ -f "$logo_src" ] || { echo "make-ios-assets: missing $logo_src" >&2; exit 1; }

command -v magick >/dev/null 2>&1 || { echo "make-ios-assets: needs ImageMagick (brew install imagemagick)" >&2; exit 1; }
command -v rsvg-convert >/dev/null 2>&1 || { echo "make-ios-assets: needs librsvg (brew install librsvg)" >&2; exit 1; }

# --- app icon --------------------------------------------------------------
# Flatten onto the brand ground rather than trusting the source: if the master
# ever gains alpha, submission fails with a message that does not name the file.
magick "$icon_src" -resize 1024x1024 -background "$BRAND_BG" -alpha remove -alpha off \
	"$assets/AppIcon.appiconset/AppIcon-512@2x.png"
echo "icon:   $(sips -g pixelWidth -g pixelHeight -g hasAlpha "$assets/AppIcon.appiconset/AppIcon-512@2x.png" | tr '\n' ' ')"

# --- launch image ----------------------------------------------------------
# 2732 square is Capacitor's convention: the widest device diagonal, so every
# phone and iPad crops rather than letterboxes. The wordmark is 1421x335, drawn
# at 1000px wide (~37% of the canvas) so it clears the crop on a narrow phone.
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
rsvg-convert -w 1000 "$logo_src" -o "$tmp/logo.png"
magick -size 2732x2732 "xc:$BRAND_BG" "$tmp/logo.png" -gravity center -composite \
	-alpha remove -alpha off "$tmp/splash.png"

# All three entries are the same image. Capacitor declares 1x/2x/3x so the
# catalog is valid on every idiom; the asset is already large enough that no
# scale needs its own file.
for f in splash-2732x2732.png splash-2732x2732-1.png splash-2732x2732-2.png; do
	cp "$tmp/splash.png" "$assets/Splash.imageset/$f"
done
echo "splash: $(sips -g pixelWidth -g pixelHeight "$assets/Splash.imageset/splash-2732x2732.png" | tr '\n' ' ')"
