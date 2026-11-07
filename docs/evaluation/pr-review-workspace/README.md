# PR review workspace fixture

These captures exercise the implemented review workspace with isolated mocked
daemon responses. They are UI evidence, not a successful live agent delivery.

- [Desktop overview](desktop.png)
- [Desktop screenshot comparison](desktop-visuals.png)
- [Narrow overview](narrow.png)
- [Narrow screenshot comparison](narrow-visuals.png)
- [WebKit overview](webkit.png)

The before/after images shown inside the workspace are existing retained Nori
pilot artifacts: `../nori-dialog-pilot/baseline/light-desktop.png` and
`../nori-dialog-pilot/continuation/light-desktop.png`. The fixture write-up labels
that provenance and the mocked CI/review values. No screenshot was generated to
stand in for a real product capture.

Headless Chromium exercised publication display, image loading, contextual
feedback drafting/sending, stale material and empty states at 1440 × 1000 and
700 × 900. It reported no page errors or horizontal document/dialog overflow.
The narrow screenshot section was checked not to overlap the actions section.
Headless WebKit rendered at 1440 × 1000 without document overflow. This does not
replace testing in the packaged native WKWebView.

Validation logs for this implementation run are in `/private/tmp/`:

- `lola-review-final-build-vet.log`: formatting, build and vet passed.
- `lola-review-frontend-check.log`: zero Svelte errors or warnings.
- `lola-review-frontend-tests.log`: 52 relevant frontend tests passed.
- `lola-review-frontend-build.log`: production frontend build passed.
- `lola-review-browser-check.log`: browser behavior and overflow results.
- `lola-review-make-check.log`: full checks reached one failing TUI setup test.
- `lola-review-baseline-test.log`: that same test fails on committed `0346cb6`.

The temporary preview server used no live daemon and was stopped after capture.

## Interactive canvas refinement

- [Overview tab](overview-tab.png)
- [Canvas and comparison divider](canvas.png)
- [Area annotation and inline draft](canvas-annotations.png)
- [Narrow canvas](canvas-narrow.png)

These use the same retained Nori pilot images with mocked daemon responses.
Headless Chromium exercised keyboard comparison, pointer area selection,
feedback batching and narrow layout at 1440 × 1000 and 700 × 900, with no
page errors or document/dialog overflow. The native WKWebView was not exercised
in this refinement. Component tests cover queuing/retry, draft persistence,
screen/stack isolation, comparison modes and unsupported image URLs.

The [floating comment editor](floating-comment.png) now sits beside its pin or
area on the image. Headless Chromium and WebKit exercised typing and adding
feedback; WebKit also verified containment after zooming, scrolling and narrowing
the canvas. Focused text fields use a border change without an outline/shadow
ring. The later clarification exposed a save bug: annotations were removed once
added to feedback. Saving retains the pin/area and text; reopening restores them, and editing
replaces the pending entry rather than duplicating it. The editor closes after
a successful save or click-away. Click-away saves changed text only; unchanged
comments close without resubmission, and failed saves keep the editor open.
Twenty-two component tests and Svelte checks pass, including saving, reopening,
click-away, updating before send, and following up after send.


### Compact annotation editor

The floating editor uses a short Save action, icon controls for discard/close,
and a 150ms opening transition (disabled for reduced motion). A full batch shows
an inline status and a Review feedback action that opens the pending batch while
retaining the annotation draft. It does not send feedback automatically.

Validation: 23 focused canvas/workspace tests passed; Svelte check reported no
errors or warnings. Headless WebKit checked 1440, 700, and 420px widths, editor
bounds, ring-free fields, opening feedback, and reopening the retained draft.
[Compact full-batch state](compact-comment.png) uses an isolated screenshot fixture.


### Save confirmation and comment deletion

Supersedes the full-batch editor state above. Closing or clicking away from a
changed annotation now offers Save, Discard, and Keep editing. Unchanged comments
close immediately. Every annotation has a delete icon, including saved comments;
deleting removes its exact pending entry without affecting other comments or
attempting to retract feedback already sent.

Material comments no longer share the 4,000-character save limit. The daemon
preserves the full review note and rejects rendered batches over the existing
64 KiB bound instead of silently truncating notes. A rejected send retains the
local draft. Rebuild/install and restart the daemon before sending longer notes:
an older daemon still clips notes at 4,000 characters.

Validation: 25 focused UI tests, Svelte check, daemon feedback tests, and make
build passed. Headless WebKit exercised confirmation, saving, reopening, and
deleting at 1440, 700, and 420px widths with no page errors.
[Confirmation state](comment-confirmation.png) uses an isolated screenshot fixture.


### Screenshot keyboard shortcuts

- C: comment pin; R: rectangle; V or Escape: cancel drawing.
- Command+Enter (Control+Enter on other platforms): save the open comment.
- Left/Right: previous/next screenshot; N/Shift+N: next/previous annotation.
- 1–4: slider, side by side, before, after; +/−: zoom; 0: fit.
- I: screen details; B: checks and feedback; ?: shortcut reference.
- Delete/Backspace: delete a focused annotation outside text fields.

Shortcuts operate only while the screenshot canvas is mounted. Field editing,
platform chords, IME composition, native selects, and comparison-slider arrows
retain their normal behavior. Navigation cannot bypass unsaved-change prompts.
Tool closure and annotation deletion return focus to the canvas so the next
shortcut still works. Controls expose shortcut hints and aria-keyshortcuts.

Validation: 32 focused component tests and Svelte check pass. A headless WebKit
keyboard flow covered C → Enter → typing → Command+Enter → R → Escape → N →
Escape → Delete → ?, including the reference at 420px width; no page errors.
[Shortcut reference](screenshot-shortcuts.png) uses an isolated screenshot fixture.
