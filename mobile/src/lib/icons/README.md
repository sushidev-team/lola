Icons exported from the Figma file (Lola / Mobile Redesign, file ySh4wBKGZyN06ePWBx1QQ8).

The path data is the export's, byte for byte. The only edit is the colour: every
literal the exporter baked in (`#91D7E3`, `#B8C0E0`, `#8087A2`, …) is replaced
with `currentColor`, so one glyph serves the active state, the resting state and
all four Catppuccin flavors instead of pinning macchiato into the markup. Where a
mark is KNOCKED OUT of another — the filter button's dot, the settings sliders'
handles — the knockout keeps a token (`--color-canvas` / `--color-crust`) rather
than currentColor, because it is the ground showing through and not the ink.

Two glyphs have no export because Figma drew them as rectangles rather than as a
vector: Sessions and Projects. Their geometry is transcribed from the frame's own
node metadata (nodes 49:14 and 49:23), which is the same source the exporter
would have used.

Sizes are the design's: the tab-bar glyphs are 20x22, the AI sparkle 14x16, the
branch mark 11x12.
