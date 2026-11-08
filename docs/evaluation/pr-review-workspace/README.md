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
- Command+Backspace / Command+Delete: delete the selected comment, including from its editor.
- Plain Delete/Backspace: delete a focused annotation outside text fields.

Shortcuts operate only while the screenshot canvas is mounted. Field editing,
platform chords, IME composition, native selects, and comparison-slider arrows
retain their normal behavior. Navigation cannot bypass unsaved-change prompts.
Tool closure and annotation deletion return focus to the canvas so the next
shortcut still works. Controls expose shortcut hints and aria-keyshortcuts.

Validation: 32 focused component tests and Svelte check pass. A headless WebKit
keyboard flow covered C → Enter → typing → Command+Enter → R → Escape → N →
Escape → Delete → ?, including the reference at 420px width; no page errors.
[Shortcut reference](screenshot-shortcuts.png) uses an isolated screenshot fixture.


Command-delete follow-up: both Backspace and forward Delete remove the selected
annotation from inside its editor. Unrelated fields, IME composition, and key
repeat are excluded. All 34 focused UI tests and Svelte check passed.


### Full-width review overview

The overview now uses the available width: a short summary and screenshot
previews sit beside an evidence-based confidence panel, followed by compact
feature rows, verification, and risks. Expanded rows preserve the source
Markdown. Legacy long-form packages are split into items without generating
new factual claims; summary snippets come from the supplied summary or first
change. Thumbnails open the matching screenshot in the canvas.

Review confidence is coverage of five equally weighted observed signals:
matching revision, passing CI, approved PR review, no merge conflicts, and no
unresolved review threads. Each earns 20 points. Missing or failing signals earn
none; an unknown or mismatched revision is unscored. The panel explains that
this is not a probability of correctness. Agent-reported checks, screenshots,
and risk claims are displayed separately and do not increase the score.

Validation: 40 focused tests cover parsing, exact-revision scoring, missing and
failed signals, preservation of source detail, thumbnail failures/navigation,
and existing canvas/feedback interactions. Svelte check and production build
passed. WebKit checked 1440, 700, and 420px layouts, expansion, and navigating to
the selected screenshot; no page errors or horizontal overflow.

- [Desktop overview](overview-redesign-wide.png)
- [Narrow overview](overview-redesign-narrow.png)

Captures use an isolated fixture and retained Nori evaluation images; the PR
facts in this fixture are illustrative, not observations of a live PR.


### Unified overview and prioritized risks

Supersedes the overview layout above: summary, compact features and screenshots
flow down one column; confidence, PR status/actions, risk assessment and feedback
flow down the other. Verification prose is removed. Existing live local checks
are expandable in the readiness area. The background sidebar splitter is removed
while the review (or another overlay) is open, including during an active drag.

Risk rows show explicit likelihood and impact separately. Estimates are
agent-provided, never guessed from keywords. Unrated items stay Not rated and
can request a fresh assessment without sending unsent feedback. Original detail
remains expandable. New publication instructions ask for short behavior labels,
likelihood/impact estimates and their rationale, reusing existing evidence.

- [Unified desktop overview](overview-unified-wide.png)
- [Unified narrow overview](overview-unified-narrow.png)

The isolated fixture uses retained Nori handoff-preflight images; risk ratings
and PR facts are illustrative. Headless WebKit checked 1440, 700 and 420px widths,
source expansion, screenshot navigation, and absence of the background splitter.
No page errors or horizontal document/dialog overflow. Native WKWebView was not
exercised. Sixty-four focused frontend tests, Svelte check, production build and
Go runtime tests passed. Impeccable's layout scan reported no findings.


Panel styling follow-up: Changes, screenshots, risks and feedback now share the
readiness panel's bordered surface, inset spacing and rounded corners. Feature
counts and risk levels have compact badges; expanded rows show their active
state. Updated captures above were checked in WebKit at 1440, 700 and 420px with
no browser errors or horizontal overflow. All 22 overview/workspace component
tests and Svelte check passed. This is a frontend-only styling change.


### Feature groups and concise risk expansions

Changes now show what is new, grouped into a lead feature and supporting
features, with user-facing sub-features on expansion. No before/after text or
source-note disclosure appears in these groups. Risks expand to just What could
happen and How to assess it, without repeating headings, ratings or source prose.
Legacy After fields remain readable as the new-capability sentence.

The requested agent rewrote and published CE-159 / coachkit-app PR #112 material:
package `9840d6d3267317fa52328f294823dba22d54df359cdaa15b418af8cf3e56dfbd`,
head `947d78351123ccdc18254890cf9d2cd0ac03e989`. The preview uses this actual
retained write-up and screenshots; readiness responses remain isolated fixtures.
Risk ratings are the agent's estimates, not an independent verification here.

- [Expanded features and risk](overview-features-wide.png)
- [Narrow feature layout](overview-features-narrow.png)

31 focused tests, Svelte check and production build passed. WebKit checked
1440, 700 and 420px widths, source-free feature/risk expansions and screenshot
navigation with no browser errors or horizontal overflow. Runtime spawn tests
also passed for the updated publishing instructions.

The version control stays one line at the standard 32px height and shows only
Latest for the newest package. Publication date/time and the smaller commit ID
remain in its menu. Native disclosure markers were replaced across the UI with
shared drawn chevrons; a source guard prevents bare summary markers returning.
Final validation: all 1,872 frontend tests, Svelte check, and production build
passed. WebKit confirmed the compact trigger, chevrons, and responsive layout.


### Simplified review styling

- [Restyled overview](overview-restyled-wide.png)
- [Narrow action-first layout](overview-restyled-narrow.png)
- [Light theme](overview-restyled-light.png)

The overview now uses compact expandable change rows, a quieter screenshot strip,
and a distinct review/action column. Evidence coverage uses violet; the primary
merge action uses cyan. Impact stays visible while likelihood and assessment
instructions expand on demand. Small windows place review actions immediately
after the summary. Screenshot images are contained without cropping.

Captures use the retained CE-159 write-up and screenshots described above, with
mocked PR readiness. They do not claim that the real PR passed checks or merged.
WebKit verified disclosures, screenshot navigation and no horizontal overflow at
1440, 1024, 700 and 420px. Sampled light-theme text/action contrast is at least
4.5:1. Fifty focused frontend tests, Svelte check (zero errors/warnings), and the
production frontend build passed. The design detector reported no findings.
The packaged native app was not rebuilt or restarted for this styling pass.


### Feature hierarchy refinement

What changed restores the grouped lead/supporting feature layout and visible
short descriptions. The panel heading is 20px/600, feature titles 15px/500, and
descriptions 13px/400 with secondary ink. The count sits beside the heading;
cyan icon tiles and consistent indentation organize the feature content.

- [Updated desktop hierarchy](overview-hierarchy-wide.png)
- [Updated narrow hierarchy](overview-hierarchy-narrow.png)

WebKit verified computed typography and overflow at 1440, 1024, 700 and 420px.
Four overview tests, Svelte checks and the production build passed. Fixture
provenance and native-app limitations remain as described above.


### Risk comparison refinement

Restored visible likelihood and impact columns with compact level bars and
explicit Not rated states. The panel now matches the stronger feature-section
heading and count badge. A wider proportional decision column supports scanning;
narrow panels keep both ratings below each title with their labels repeated.
Descriptions and assessment steps remain expandable.

- [Risk comparison on desktop](overview-risks-wide.png)
- [Risk ratings in a narrow window](overview-risks-narrow.png)

WebKit verified both ratings remain visible with rows collapsed, expansion works,
and no horizontal overflow occurs at 1440, 1024, 700 or 420px. Dark and light
palettes were inspected. Four overview tests, Svelte checks and production build
passed. Captures retain the same real material and mocked PR-readiness provenance.
