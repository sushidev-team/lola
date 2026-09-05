---
target: General and project settings
total_score: 26
max_score: 40
na_heuristics: 
p0_count: 0
p1_count: 1
target_identity: "file:desktop/frontend/src/lib/views/SettingsForm.svelte"
target_fingerprint: "sha256:058656c8c0328cebe3dc5423c3a6bd4f68cea4d606eb1f06a5c91e7fde0e5c31"
target_path: desktop/frontend/src/lib/views/SettingsForm.svelte
timestamp: 2026-09-05T08-23-44Z
slug: desktop-frontend-src-lib-views-settingsform-svelte
closed: true
---
Method: dual-agent (A: /root/design_review · B: /root/detector_review)

Impeccable 4.2.0 critique: general and project settings
Scope: SettingsForm.svelte, ProjectForm.svelte, and shared control source. No current browser verification. The supplied screenshot predates current label changes.

Design specificity: Clearly Lola-specific. Folder-derived setup, inherited defaults and Linear lifecycle grouping suit an operator tool. Preserve this direction; improve consistency and feedback.

Provisional health: 26/40 — Acceptable.

| Heuristic | Score /4 | Finding |
|---|---:|---|
| System status | 3 | Loading visible; errors lack live announcements |
| Real-world match | 2 | Global and project terminology differs |
| User control | 3 | Dirty-close guards; override drafts lost on reset |
| Consistency | 2 | Same settings use different labels and controls |
| Error prevention | 2 | General limits defer validation until Save |
| Recognition | 3 | Presets help; required formats hidden |
| Efficiency | 3 | Keyboard navigation; long label lists lack search |
| Minimalism | 3 | Useful sections; some irrelevant choices remain |
| Error recovery | 2 | Preserves edits; no metadata retry |
| Help | 3 | Contextual help; some essential instructions hidden |

Strengths: Info beside labels with inheritance underneath is now correct. Folder-first setup and advanced identity disclosure reduce initial decisions. Both dialogs protect unsaved edits; tabs support arrows and the modal restores focus.

Priority issues:
1. P1 — An Override status badge actually resets the field to defaults and loses its custom draft. ProjectForm.svelte:209 and :467. Separate state from action: Project override / Use default; retain the draft when switching back or offer Undo. /impeccable harden.
2. P2 — Defaults and project settings name identical concepts differently: Dedup mode versus Repeat pickup, On-sent set label versus After pickup label. SettingsForm.svelte:960 and ProjectForm.svelte:867. Share labels and options, and clearly identify General agent/limit values as project defaults. /impeccable clarify.
3. P2 — General total-agent input permits zero although internal/config/validate.go:119 rejects it. SettingsForm.svelte:807. Validate positive whole numbers inline and announce errors; project already has a stronger limit control. /impeccable harden.
4. P2 — Essential textarea syntax is hidden in detail-only popovers. SettingsForm.svelte:731, ProjectForm.svelte:512. Keep One command per line and KEY=value, one per line visible below fields; keep lifecycle explanations beside labels in info popovers. /impeccable clarify.
5. P2 — Failed Linear metadata fetches offer UUID inputs without Retry. SettingsForm.svelte:158 and :921, ProjectForm.svelte:824. Add Retry, preserve selections and make manual IDs an explicit fallback. /impeccable harden.

Cognitive load: Moderate. Chunking, visible option count and cross-screen terminology remain weak. The seven-provider add list and unfiltered workspace labels warrant grouped/searchable selection when large. The nine global navigation items are already usefully grouped.

Personas: First-time operators must translate config vocabulary and discover entry syntax. Power users cannot safely compare custom/default values without losing a draft. Screen-reader users have keyboard navigation but no live error announcements or field-specific inheritance button names.

Emotional journey: Folder detection reassures; unexpected resets and UUID fallback erode confidence. Discard protection helps, but save errors still require diagnosis.

Minor observations: Use Creation time instead of createdAt in priority choices. Explain or conditionally reveal After pickup label when relevant. Verify narrow-window and increased-zoom layout before claiming overflow.

Detector: exact JSON [], exit 0, zero findings/advisories across both Svelte files. This is a source-pattern scan, not proof of usability or accessibility. Browser unavailable: Playwright default Chromium missing; cached Chromium aborted before page creation. No live overlays, screenshots or console evidence. No UI source edited during this review.

Recommended next direction: safe overrides and validation, followed by consistent wording and visible syntax hints, metadata recovery, then polish.
