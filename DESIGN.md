---
name: Lola desktop
description: Compact operational desktop UI with optional Midnight colors
colors:
  mocha-canvas: "#181825"
  mocha-panel: "#1e1e2e"
  mocha-ink: "#cdd6f4"
  mocha-accent: "#89dceb"
  midnight-canvas: "#121624"
  midnight-panel: "#191d2f"
  midnight-ink: "#e6e9f5"
  midnight-cyan: "#7fddea"
  midnight-violet: "#b986ff"
typography:
  body:
    fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif'
    fontSize: "13px"
    lineHeight: "18px"
  reading:
    fontSize: "14px"
    lineHeight: "22px"
  metadata:
    fontSize: "12px"
    lineHeight: "16px"
  section:
    fontSize: "15px"
    lineHeight: "20px"
    fontWeight: 600
  title:
    fontSize: "18px"
    lineHeight: "24px"
    fontWeight: 600
rounded:
  panel: "10px"
  overview: "9px"
components:
  overview-card:
    backgroundColor: "color-mix(in srgb, var(--color-panel) 80%, var(--color-canvas))"
    textColor: "var(--color-ink)"
    rounded: "{rounded.overview}"
    padding: "15px"
---

# Design System: Lola desktop

## Overview

Lola uses a compact operational interface: quiet layered surfaces, readable conversation text, and semantic color for state and navigation. This is a scoped source record of the existing desktop system and the optional Midnight extension, not a replacement visual identity.

Sources: `desktop/frontend/src/app.css`, `desktop/frontend/src/lib/catppuccin.ts`, `desktop/frontend/src/lib/components/Button.svelte`, `desktop/frontend/src/lib/tooltip.ts`, and the session activity and overview components. Theme integration also lives in `internal/tui/catppuccin.go`, `internal/config/ui.go`, and `desktop/main.go`.

No root PRODUCT.md or earlier DESIGN.md was available. This record is based on implementation. The user confirmed the single-row layout from a screenshot, but that capture predates the tooltip stacking fix; final visual review still requires recapture because browser preview was unavailable. This record does not certify rendered layout or contrast.

## Colors

### Primary

Cyan identifies actions and focus. Components use semantic runtime tokens, so the configured palette controls chrome, terminal colors and pane snapshots together.

### Secondary

Violet distinguishes agent activity. Orange marks file activity, cyan commands, and green test commands. Labels and statuses provide meaning beyond color.

### Neutral

The compiled default remains Catppuccin Mocha. Midnight (`lola-midnight`) is an optional navy palette with brighter reading ink, cyan and violet. Existing saved theme choices remain valid. Other Catppuccin palettes continue to resolve through the same runtime tokens.

For this installation, the user explicitly selected Midnight in place of the saved Macchiato preference. This is a saved local choice, not a change to the default for other installations.

**The Theme Choice Rule.** Add palettes through the shared theme mapping; preserve the default and saved preferences.

## Typography

Use the system sans stack for the desktop interface, the configured monospace stack for code, and bundled Hack for terminals. The token ramp separates metadata, ordinary controls, reading text, section titles and view titles. Conversation reading text uses the reading role. Compact overview headings retain their local 13px/20px treatment; they are not a new global heading level.

## Layout

Conversation and session overview remain separate working areas. Overview sections use compact stacked cards with 12px gaps. A zero-minimum-width grid column (`minmax(0, 1fr)`) and constrained descendants keep long labels within the overview width. Nested tool rows retain compact, borderless rows with a 28px minimum height and their own columns. The activity strip is navigation within the conversation, with no independent header, filter or playback controls. Its narrower container treatment reduces horizontal padding at 520px.

## Elevation & Depth

Tonal surface changes and thin borders carry most depth. Overview cards add a faint inset highlight. Activity hover and focus brighten the segment, while keyboard focus adds a cyan outline. Reduced-motion preferences disable the strip's color and shadow transitions.

## Shapes

Panels use gently rounded corners; overview cards use their own compact radius. Buttons retain the shared component's small rounded shape and 24/28/32px height ladder. These are desktop density choices, not a mobile touch-target specification.

## Components

### Buttons

Reuse `Button.svelte`: ghost, accent, secondary, primary, danger, danger-solid and bare variants share density and disabled behavior. Primary actions use a tinted accent background and readable accent text. Selected controls reuse the same tint; disabled controls do not acquire hover treatment.

### Activity navigation

The strip is one chronological row. Recorded execution duration weights each entry's width; running entries use elapsed time. Pauses become small separators, capped at 5px each and constrained by the entry count, rather than consuming proportional time-axis space. Concurrent calls remain consecutive, individually navigable entries. Segments fill a 28px track vertically inside a 3px inset.

The strip jumps to the corresponding conversation entry and reveals it in the full conversation filter. Accessible names include the action and timestamp, plus recorded duration when available. Durationless entries retain a visible minimum width without pretending they have measured duration. Arrow keys, Home and End move between entries; selection and focus have visible states. Failed entries add a red bottom border (2px).

Activity-map tooltips explicitly opt into immediate display on hover or focus and show the action category, status, duration and preceding pause when known. Other shared tooltips retain their 300ms delay. Only one tooltip is visible at a time across both modes. A short leave tolerance lets the pointer enter and scroll the card; Escape dismisses it.

### Session overview

Changes display cached file/addition/deletion totals from the changes view, with an explicit unloaded state. Tests describe agent-reported command runs from loaded history, not test-case counts or independently verified CI. Recent activity shows the latest five actions, each navigating to its conversation entry. Existing reported plans, findings, theories, notes and widgets remain available.

**The Evidence Label Rule.** Distinguish observed diff totals from agent-reported commands and plans, and retain honest unloaded states.

## Do's and Don'ts

- **Do** reuse semantic color tokens and shared controls across palettes.
- **Do** keep conversation reading text distinct from compact metadata.
- **Do** retain keyboard navigation and visible focus on activity entries.
- **Don't** present command-run completion as a count of passing test cases.
- **Don't** treat this source record as final visual approval.
