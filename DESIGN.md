---
name: Lola desktop
description: Persistent session workspace with resizable sidebar and optional Midnight colors
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
  dialog-title:
    fontSize: "18px"
    lineHeight: "28px"
    fontWeight: 600
    letterSpacing: "-0.02em"
  dialog-body:
    fontSize: "14px"
    lineHeight: 1.55
  dialog-detail:
    fontSize: "13px"
    lineHeight: 1.55
rounded:
  dialog: "16px"
  dialog-action: "8px"
  panel: "10px"
  overview: "9px"
  session-row: "6px"
  session-search: "5px"
components:
  dialog-window:
    backgroundColor: "var(--color-panel)"
    textColor: "var(--color-ink)"
    rounded: "{rounded.dialog}"
  dialog-confirmation:
    backgroundColor: "var(--color-panel)"
    textColor: "var(--color-ink)"
    rounded: "{rounded.dialog}"
    width: "480px"
  dialog-title:
    typography: "{typography.dialog-title}"
  dialog-confirmation-body:
    typography: "{typography.dialog-body}"
    textColor: "var(--color-ink)"
  dialog-confirmation-detail:
    typography: "{typography.dialog-detail}"
    textColor: "var(--color-ink-2)"
  dialog-action:
    rounded: "{rounded.dialog-action}"
    padding: "8px 12px"
  overview-card:
    backgroundColor: "color-mix(in srgb, var(--color-panel) 80%, var(--color-canvas))"
    textColor: "var(--color-ink)"
    rounded: "{rounded.overview}"
    padding: "15px"
  session-navigation-row:
    textColor: "var(--color-ink)"
    rounded: "{rounded.session-row}"
    padding: "7px 10px"
  session-search:
    backgroundColor: "var(--color-panel)"
    textColor: "var(--color-ink)"
    rounded: "{rounded.session-search}"
    height: "30px"
    padding: "4px 26px 4px 9px"
---

# Design System: Lola desktop

## Overview

Lola uses a compact operational interface: quiet layered surfaces, readable conversation text, and semantic color for state and navigation. This is a scoped source record of the existing desktop system and the optional Midnight extension, not a replacement visual identity.

Sources: `desktop/frontend/src/app.css`, `desktop/frontend/src/lib/catppuccin.ts`, `desktop/frontend/src/lib/components/Button.svelte`, `desktop/frontend/src/lib/tooltip.ts`, and the session activity and overview components. Workspace navigation is defined in `App.svelte`, `Sidebar.svelte`, `SidebarRoster.svelte`, `SidebarSessionTools.svelte`, `SidebarResize.svelte`, `SessionsColumn.svelte`, `lib/roster.ts`, `lib/sidebarwidth.ts` and `lib/nav.svelte.ts`. Theme integration also lives in `internal/tui/catppuccin.go`, `internal/config/ui.go`, and `desktop/main.go`.

No root PRODUCT.md or earlier DESIGN.md was available at the initial extraction. This record is based on implementation. The earlier user-confirmed activity-strip screenshot predates the tooltip fixes and sidebar workspace redesign. No current visual capture or approval is available; this record does not certify rendered layout or contrast.

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

The desktop uses one full-height session workspace with persistent sidebar navigation. List, Kanban and terminal-grid lenses are removed from the desktop. Sidebar collapse changes the layout track without unmounting it; switching auxiliary views preserves the mounted session workspace beneath the view. Sessions and Projects tabs retain their own mounted content.

The sidebar preference defaults to 320px and is stored locally in the webview, bounded to 240–480px. Available window width further limits the rendered sidebar to preserve 560px for the workspace, so a narrow window can render below the preferred minimum. Drag the separator to resize; arrow keys change width by 10px, Shift increases the step to 40px, Home/End select bounds, and double-click restores the default. Hover, drag and keyboard focus expose the cyan resize edge. At a sidebar container width of 275px or less, controls reduce horizontal padding and rows reduce column gaps.

Conversation and session overview remain separate working areas. Overview sections use compact stacked cards with 12px gaps. A zero-minimum-width grid column (`minmax(0, 1fr)`) and constrained descendants keep long labels within the overview width. Nested tool rows retain compact, borderless rows with a 28px minimum height and their own columns. The activity strip is navigation within the conversation, with no independent header, filter or playback controls. Its narrower container treatment reduces horizontal padding at 520px.

## Elevation & Depth

Tonal surface changes and thin borders carry most depth. Overview cards add a faint inset highlight. Activity hover and focus brighten the segment, while keyboard focus adds a cyan outline. Reduced-motion preferences disable the strip's color and shadow transitions.

Windowed dialogs use a subdued semantic edge and two soft offset shadows, recorded in the sidecar. Their brief entrance is disabled under reduced motion.

## Shapes

Panels use gently rounded corners; overview cards use their own compact radius. Buttons retain the shared component's small rounded shape and 24/28/32px height ladder. These are desktop density choices, not a mobile touch-target specification.

## Components

### Icons

Use the official `@lucide/svelte` library through `Icon.svelte` in desktop and mobile. `icons.ts` contains explicit per-icon imports; extend that curated set instead of importing the full catalog. The default is 16px with a 2-unit stroke, 14px for compact sidebar and metadata icons, and 22px for mobile navigation. Icons inherit semantic text colors. `GlyphIcon` adapts existing status strings at the GUI boundary; daemon and terminal vocabularies remain unchanged. Decorative icons are hidden from assistive technology; icon-only actions need a meaningful accessible name on the control. Logos, QR codes, chart marks and terminal key labels are not UI icons.

### Buttons

Reuse `Button.svelte`: ghost, accent, secondary, primary, danger, danger-solid and bare variants share density and disabled behavior. Primary actions use a tinted accent background and readable accent text. Selected controls reuse the same tint; disabled controls do not acquire hover treatment.

### Dialogs

`Modal.svelte` gives every windowed dialog the same continuous panel surface, dialog radius and title role, with no header or footer dividers. The horizontal inset and header top inset are 24px, becoming 20px at viewport widths of 480px or less. Default bodies have 16px top and 24px bottom padding (14px top when narrow); explicit custom body layouts retain their padding with 16px separation below the header. Footers share the panel surface and bottom inset. The shell retains a 16px viewport margin; fullscreen treatment is unchanged.

Shared `dialog-actions` rows place quiet Cancel left and the relevant primary, secondary or destructive actions right. Actions wrap, use a 36px minimum height, and become full-width stacked controls with a 40px minimum height at the narrow breakpoint. Standard footer buttons share the desktop sizing. Confirmations retain their width and copy roles, optional secondary alternative, solid destructive action and safe initial Close focus. Rename uses the same 480px width, a themed field with an accessible hint, and footer Cancel / Save name actions linked to the native form. New-session and SSH forms use the same action rows. Long labels wrap; focus trapping, restoration, guarded dismissal, native submission and error retention remain intact.

The scoped dialog review covered confirmation, rename, new-session and form-dialog captures, including narrow and light-theme examples, in `.impeccable/review/dialogs/`; that evidence applies to these shared components only.

### Sidebar session navigation

Search and the All / Needs you / Ended tabs sit above the grouped roster; an active project scope can be cleared separately. Live-session search matches all entered words across title, issue, branch, session identifier, project label and PR number. Groups retain configured project order and stable session ordering rather than reordering on status changes. Project headings toggle their session rows with a chevron and count, without closing the active workspace. Each search change reopens groups to reveal results. The + beside search offers new session, SSH session, and temporary shell creation with keyboard menu navigation. Empty SSH and Temporary sections are omitted; existing sessions in those groups remain listed. The lower waiting/running summary is removed.

Live rows use up to three lines: title, delivery metadata, and current activity, with a 70px minimum height. Routine statuses use a compact 54px two-line layout, with exit/age information inline beside the issue and generic waiting notifications available in the tooltip. Review badges already convey the routine waiting state, so it is not repeated below them. Meaningful updates, blockers, and questions retain their activity line. Rows without distinct metadata also use two lines; an issue identifier already used as the title and a status already present in the activity line are not repeated. Sidebar status marks use a consistent circular Lucide badge at 14px with a 2-unit stroke: cyan quarter-filled working ring, filled amber attention badge, quiet resting outline, cyan completed-turn check, muted exited minus-circle, dashed orphaned question mark, and terminal. The fixed working segment signals activity rather than a measured completion percentage. Resting sessions awaiting review use a violet three-quarter-filled circle and omit the repeated review text; working and needs-you marks retain priority over PR state. Exited and orphaned rows use quieter title weight. Titles use a semibold 13px role above 11px delivery metadata and 12px activity text. Project headings use sentence case and 18px separation between groups. Selected rows use the selected-surface fill with a fine cyan-tinted inset outline; hover uses a quieter tint and keyboard focus adds a cyan outline. A compact, borderless 18px PR link with a 12px icon opens the pull request directly when its URL is known; a separate ellipsis exposes the existing session menu on hover or keyboard focus. Both actions are siblings of the session-selection button, so neither changes the active workspace. Dev-active sessions show a 6px green indicator inset 6px from the upper-right corner; the menu leaves that corner clear. Truncated text remains available through the shared delayed tooltip. The session context menu is limited to management: rename, reveal folder, switch agent, end/revive, and relevant recovery or live SSH connection actions. Workspace navigation, dev URLs, review/report controls, and opening preferences stay in their dedicated surfaces. The CodeRabbit-specific poll is available through command search.

Ended sessions have a dedicated tab with a scoped count, search, and the same row keyboard navigation; returning to All leaves the open conversation intact. Selecting a row opens its session without toggling it closed. Arrow keys or j/k move through the visible roster; Home/End or g/G reach its ends. Escape follows the existing overlay and editor dismissal order, then returns focus to the sidebar rather than closing the workspace. In sidebar search, Escape clears the query.

### Activity navigation

The strip is one chronological row. Recorded execution duration weights each entry's width; running entries use elapsed time. Pauses become small separators, capped at 5px each and constrained by the entry count, rather than consuming proportional time-axis space. Concurrent calls remain consecutive, individually navigable entries. Segments fill a 28px track vertically inside a 3px inset.

Clicking or dragging across the strip navigates the full conversation filter without opening disclosures. A hidden entry lands on its nearest visible activity group, command group, turn, or earlier-session summary. Search and evidence links retain their reveal behavior. Pointer capture supports scrubbing beyond the strip edges, deduplicates moves within one entry, and ends on release or cancellation; tooltips stay hidden during a drag. Accessible names include the action and timestamp, plus recorded duration when available. Durationless entries retain a visible minimum width without pretending they have measured duration. Arrow keys, Home and End move between entries; selection and focus have visible states. Failed entries add a red bottom border (2px).

Activity-map tooltips explicitly opt into immediate display on hover or focus and show the action category, status, duration and preceding pause when known. Other shared tooltips retain their 300ms delay. Only one tooltip is visible at a time across both modes. A short leave tolerance lets the pointer enter and scroll the card; Escape dismisses it.

### Session overview

Changes display cached file/addition/deletion totals from the changes view, with an explicit unloaded state. One Checks section has Local and PR tabs. Local combines the agent’s reported checks (including summaries, evidence verdicts, and report age) with recent test commands from loaded history. Reported results and command outcomes retain separate labels and counts. PR shows cached GitHub CI and review status, freshness, app PRs, overlaps, and the review action. Local checks use evenly aligned icon/name/result rows with light separators and subordinate summaries. Each row has one result (passed, running, failed); positive evidence is available on hover, while missing or contradictory evidence remains visible below the result. Timestamps are quiet 11px metadata. The PR tab leads with the app name and one PR link, then aligned CI and review rows, with overlaps and other apps below. The focus PR is not repeated among other apps. Review action and GitHub refresh time sit in the footer. Unknown or missing CI status never becomes a pass. The old embedded local-check list is hidden in the overview, while standalone agent reports retain it. Dev controls and local URL links live in the overview; the session header contains identity and session actions. Recent activity shows the latest five actions, each navigating to its conversation entry. Existing reported plans, findings, theories, notes and widgets remain available.

**The Evidence Label Rule.** Distinguish observed diff totals from agent-reported commands and plans, and retain honest unloaded states.

## Do's and Don'ts

- **Do** reuse semantic color tokens and shared controls across palettes.
- **Do** keep conversation reading text distinct from compact metadata.
- **Do** retain keyboard navigation and visible focus on activity entries.
- **Don't** present command-run completion as a count of passing test cases.
- **Don't** treat this source record as final visual approval.


### Shared session tasks

Tasks live in the Overview sidebar as a compact outline, never above the conversation. Active tasks are single-line rows, with in-progress tasks first. A row expands to show its full title, status controls, and indented requirements. Requirement counts remain visible when folded; completed tasks sit behind one expandable Completed row. Tasks and Requirements are adjacent tabs. Requirements contains unassigned entries and deliberate global constraints; task-specific criteria remain nested under tasks. Add follows the active tab, and each task offers Add requirement with its parent preselected. Status is a small circular icon with one-third, two-thirds and full fills for To do, In progress and Done. Clicking it opens a compact keyboard-accessible picker. Requirements are single-line rows that expand for their full text and quiet edit, remove or assignment actions; no select fields occupy the list.

Shared tasks persist with the daemon session and survive replacement or clearing of the agent's reported plan. User additions and edits enter the conversation immediately through its existing delivery/queue path. Failed messages leave the saved card visible with a retry action. Agents can read, add and update tasks using `lola report task`; they cannot rewrite or remove user-authored requirements. Task status is reported progress, never a signal for session lifecycle or human acceptance.

The Overview no longer repeats session identity and token totals. Token usage lives in the header, with start time in the title's facts hover card. Completed agent plans fold to a single expandable row; blockers remain expanded. The entire Changes summary is one focusable navigation target.

The task hierarchy is a durable agreement, separate from the agent's replaceable plan. Agent-created requirements must name a task or explicitly use --global for a session-wide constraint. The agent reads current scope, reuses existing IDs, groups criteria under the outcome responsible for verifying them, and marks each criterion complete before completing its task. New or reopened criteria reopen a completed parent. Agents cannot rewrite user scope or remove a parent while leaving children unassigned. Launch instructions, user-item messages and `lola report task help` share one operating contract in `internal/taskboard/briefing.go`.

An inline terminal shares the remaining workspace height with conversation history so its prompt stays above the composer.

Form fields share quiet borders, regular-weight labels, and a restrained accent focus ring. Select controls retain native form bindings and validation while presenting a themed, keyboard-accessible listbox instead of the operating-system popup.

### Disclosure controls

Use the shared `Chevron` component (or an existing drawn chevron icon) for every
expandable summary. Never use browser-native disclosure triangles or text arrow
glyphs. Native `details` semantics and keyboard behavior stay intact; the chevron
rotates with open state. Action-menu summaries can retain their ellipsis icon.


### PR review workspace

The review overview uses a quiet canvas with a short behavior summary and a
grouped What changed panel, followed by screenshot previews. Its 20px semibold
heading and count badge sit above 15px feature titles and 13px muted descriptions.
Descriptions remain visible; supporting sub-features expand. The first feature
spans the panel, with supporting features in two columns when space permits.
Source revision and score methodology stay in disclosures. The risk panel shares
the feature panel’s heading, count badge and surface treatment. Likelihood and
impact are always visible as aligned rating badges with miniature level bars;
unknown values remain Not rated. Wide panels use comparison columns, while narrow
panels place both labelled ratings below the risk title. Assessment prose expands
on demand. Ratings remain explicitly agent-provided. A proportional decision
column gives the risk comparison enough room beside the changes and screenshots. Violet identifies
evidence coverage, cyan carries the merge action, and semantic green/amber retains
its status meaning. The score describes evidence coverage, never correctness.
Shared palette tokens preserve light and dark themes. At narrow widths, the review
actions follow the summary, before changes and screenshots. Screenshot previews
keep their full image visible and open the existing comparison canvas. The scoped
BeforeMerge presentation leaves the ordinary session overview unchanged.
