# PR review workspace

Lola should make agent-produced work easy to inspect, correct, and finish. A
full-screen review workspace is the next product priority: a retained write-up,
before/after screenshots, code changes, verification, and merge readiness in one
place. It extends the existing session and Changes view.

## Priority and scope

1. Ship agent-published review packages and the desktop review workspace.
2. Strengthen revision-bound verification and the feedback/re-review loop there.
3. Validate conversation/completion support across Claude, Codex, and OpenCode.
   Package publication must work independently of transcript readers from day one.
4. Productize the automation API around concrete publishing, feedback, and
   completion operations.
5. Add browser-assisted capture, then acknowledgment controls, schedules, and
   remote execution as usage justifies them.

The first release includes a Markdown write-up, captioned before/after screenshot
pairs with full-size inspection, existing diffs, local checks and merge readiness,
and review feedback sent to the agent. It does not require an embedded browser,
component-to-source mapping, executable agent HTML, or a new merge engine.

## Review experience

Enter Review from the session's Changes view and PR context. The workspace fills
the available application content area and can expand from the compact session.
Retain a direct route back to the conversation and terminal.

- **Write-up:** what changed and why, affected flows, tradeoffs, known limitations,
  and manual test instructions. Label the account as agent-provided.
- **Visual changes:** screenshots grouped by screen or scenario, before and after,
  captions, viewport and page context where supplied, and full-size inspection.
  Explicitly allow a missing baseline rather than fabricating a comparison.
- **Code:** reuse existing diffs and line-comment drafting.
- **Verification:** show observed checks separately from agent claims; reuse local
  checks and the goal evidence concepts without granting ordinary reports the
  authority of independently executed goal verification.
- **Readiness:** reuse Before merge, current GitHub threads, CI, conflicts, and
  merge policies. A package never authorizes merging or marks a check passed.

The reviewer can collect comments and send them as a batch. Material comments must
carry the package identity, revision, and screenshot/section context. After a fix,
show that new work needs review; retain the prior material and feedback context.
Screenshot-region annotations and a dedicated changes-since-review comparison are
follow-up work.

## Publishing contract and storage

Provide a small agent-facing CLI that accepts a structured manifest containing a
Markdown write-up, base/head commit identities, and named screenshot pairs. The
CLI sends the manifest through the existing daemon transport, including the
authenticated container hook bridge. The daemon copies images from the known
session checkout using relative paths, so image bytes do not enlarge the socket
request limit. All three coding agents use the same contract.

The daemon binds publication to the submitting session, validates bounded input,
and stores an immutable package outside the disposable worktree. Record a package
identifier, session, repository/PR context when available, creation time, base and
head revisions, artifact metadata, and content hashes. Publishing an update creates
a new package; it does not rewrite the one a reviewer already inspected. Retained
material remains readable after worker cleanup.

Only supported raster images are accepted initially. Validate actual file content,
dimensions and size; reject unsafe paths, unsupported types and oversized payloads.
Render Markdown with Lola's existing safe renderer. Do not execute agent-provided
HTML or let an arbitrary artifact path become a filesystem read endpoint.

Bind ordinary material to commits and explain its provenance honestly: agent-
provided commit identities and screenshots are claims, not independent proof of
what ran. Compare the package head with the currently observed PR head and label
old material stale. Unknown freshness must not be presented as current. A clean
commit identifier alone cannot attest to screenshots taken from dirty files.

Human reads and feedback retain existing command boundaries. Publication cannot
invoke human-only commands, change agent/delivery state, consume concurrency slots,
or bypass send gates. Keep GitHub publishing an explicit separate action; local
review does not silently post comments or upload images.

## Implementation sequence

### 1. Retained publication and inspection

- Add a small storage leaf with immutable bounded packages and validated images.
- Add protocol commands, session-bound daemon handling, and an agent CLI with
  machine-readable receipts and an example manifest.
- Wire the publication route through the existing authenticated sandbox transport.
- Teach spawned agents how and when to publish material.
- Test invalid input, artifact retention, session isolation, repeated publication,
  and restart readback using isolated temporary stores.

### 2. Desktop review workspace

- Add a service client and Review entry alongside Changes without duplicating
  terminal/session mounts or altering the sidebar's stable ordering.
- Render write-up, screenshot pairs, package history and freshness states.
- Reuse diff feedback, Before merge and local checks; add contextual material
  feedback and a clear empty state with publication guidance.
- Preserve feedback drafts during navigation and reject stale async responses.
- Verify keyboard navigation, narrow layouts, image inspection, missing artifacts,
  old-daemon errors, and package updates while a review is open.

### 3. Evidence and re-review

- Pin observed verification to exact revisions and visibly invalidate stale checks.
- Add screenshot-region annotations and section-anchored comments.
- Track the reviewed package explicitly and expose changes since that review.
- Add optional browser capture with URL, viewport, and selected-element context.

## Acceptance and validation

A coding agent can publish a write-up and screenshots, the human can inspect them
in Lola, compare them with code and live PR facts, send contextual feedback, and
inspect a new package after the correction. Old material survives session cleanup
and cannot silently acquire a new revision identity.

Use storage and daemon tests for isolation and transport boundaries, CLI tests for
the publishing contract, and component tests for review navigation and feedback.
Run `make check`, frontend type checks, relevant frontend tests, and the production
frontend build. Inspect a browser-rendered fixture without synthetic OS input;
record separately any native WKWebView verification still outstanding.

## Status

- Plan committed before implementation as `0346cb6`.
- Implemented: immutable publication and retained image capture; agent CLI and
  launch instructions; sandbox session-token authorization; desktop full-screen
  Review from Changes and PR status; Markdown and screenshot pairs; image zoom;
  package history and freshness; shared code/material feedback; retained review
  access from ended sessions, including stack member packages.
- A new publication does not replace the revision currently being inspected.
  Material comments retain package, base/head, section and screenshot identities.
  Drafts survive navigation; failed sends retain the batch.
- Revision-specific independent verification, browser capture and a changes-since-review comparison remain follow-up work.
- Current code diffs explicitly represent the live checkout, not a frozen package
  diff. Existing local-check provenance and merge policy are unchanged.
- Initial storage limits: 64 KiB manifest; four screenshot pairs; PNG/JPEG only;
  4 MiB and 20 megapixels per image; 20 packages or 128 MiB per session, shared by
  its stack members. Reaching a limit refuses publication rather than deleting
  earlier review evidence. A retention-management UI is not included yet.
- Validation: build, vet, formatting, publication/storage/CLI regressions, 52
  relevant frontend tests, Svelte checks (zero errors/warnings), production frontend
  build, and isolated Chromium/WebKit fixtures. `make check` reaches one unrelated
  failure, `TestSetupGeneralSettingsAreOptionalAndEditable`; the same assertion
  fails on committed `0346cb6` without this implementation.
- Native packaged WKWebView and a live coding-agent publication have not been
  exercised. The running daemon has not been installed or restarted.

## Using the first release

Run inside a Lola agent session. Capture images under the checkout, then write a
manifest such as:

```json
{
  "version": 1,
  "title": "Settings form spacing",
  "body": "## What changed\nExplain the change and why.\n\n## Verification\nName checks actually run and any limitations.\n\n## Manual review\nDescribe the flow to inspect.",
  "base": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "head": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
  "screens": [
    {
      "title": "Settings",
      "before": ".lola/review-before.png",
      "after": ".lola/review-after.png",
      "caption": "Explain the visible difference.",
      "viewport": "1440 × 900",
      "url": "http://localhost:3000/settings"
    }
  ]
}
```

Replace the example hashes with full commit hashes for the captures. Paths are
relative to the session checkout, not the manifest directory. Omit `before` when
no baseline exists; use `"screens": []` for nonvisual work.

```sh
lola review-package publish manifest.json
# Stack: image paths are relative to this member's checkout.
lola review-package publish manifest.json --app web
```

Success prints a JSON receipt; failure exits nonzero. Identical content returns
the original package. Publication retains material locally and never posts to
GitHub. In the desktop, select **Review PR** from Changes or PR status. Ended
sessions offer **Review material** without live checkout or send actions.

See [fixture captures and validation](evaluation/pr-review-workspace/README.md).

## Interactive review refinement

The workspace now separates **Overview**, **Screenshots**, and **Code changes**.
Overview brings confidence, live PR checks, merge/review actions, local checks,
and the feedback batch into the page. Screenshots and Code changes retain the
collapsible panel. Two independently flowing columns group the short summary,
changes and visual evidence on the left, readiness, risks and feedback on the
right; narrow windows stack them. Verification prose is no longer an overview
section. Local checks remain available on demand.

Author Changes as up to three feature groups with bold 3–8 word outcome labels,
an indented `New:` sentence describing the new capability, and `Includes:` with
2–4 nested user-facing sub-feature bullets. The overview shows the new capability
and expands into sub-features, without displaying implementation prose. Older
`After:` fields supply the new-capability sentence without comparison labels.
Risks use concrete failure-consequence headings and explicit
`Likelihood: low|medium|high|unknown; Impact: low|medium|high|unknown`, followed by
one-sentence `Outcome:`, `Assess:` (a proposed check, not a completed action), and
`Basis:`. Expanded risks show only the outcome and assessment step. Source notes
are retained in the stored package but not repeated in either feature or risk
expansions. Only explicit ratings are displayed;
legacy or unsupported ratings remain Not rated. Order uses likelihood × impact
(low=1, medium=2, high=3), with unrated items below scores 6/9 and before scores
1–4. This is an agent assessment, not independently measured probability.
**Ask agent to assess risks** requests a new package without sending pending
feedback, reusing retained evidence. The request is scoped to member and package.
The background sidebar splitter is absent while any overlay is open.

Screenshots use a full-height canvas with a collapsible screen/context rail,
compact previous/next controls, slider/split/before/after comparison, and fit/zoom.
The divider can be dragged from anywhere on the image and keyboard-operable. Pin and rectangle
annotations are relative to the original displayed image, independent of zoom.
Drafts persist locally by session/package/screen. Adding an annotation places its
text, side, normalized location, screenshot IDs, page/viewport and base/head in
the existing feedback batch; only the explicit Send action submits that batch.
The canvas is a review of retained PNG/JPEG evidence, not a live browser or
component-to-source mapper. Different capture dimensions are fitted to the same
comparison frame and may not align.

Agent launch instructions now tell agents to preserve baseline captures before
editing and accumulate review evidence while implementing and testing, ideally
with a manifest in `.lola/review/`. Publish after a meaningful verification pass
using those existing artifacts. Recreate only missing/stale evidence, never
relabel an old capture as a new revision. The empty-state request uses the same
reuse and concise-writing instructions for existing sessions. This is an explicit
agent workflow, not automatic discovery of arbitrary files or proof that an
agent followed the instruction. New launch instructions require an updated
daemon; existing sessions can use **Ask agent to prepare review**.
