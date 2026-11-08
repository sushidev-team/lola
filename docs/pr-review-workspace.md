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
implemented. Inspection is recorded separately from approval and merge authority.

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
- Re-review now records the inspected package per session/repository, retains that
  baseline across publication and cleanup, and compares changed write-up sections,
  screenshot pairs and exact published code heads. Section comments retain their
  package and section position. Screenshot-region comments were already implemented.
- GitHub evidence now carries the observed base, head, CI commit and observation
  time. A changed base/head invalidates the selected package's evidence score;
  CI receives credit only when its own commit matches. Missing revision fields
  from an older daemon remain unknown. Agent reports and transcript commands are
  explicitly unbound to package revisions and cannot supply independent evidence.
- Independent local verification now runs an explicitly authorized command against
  the retained head in a private checkout and offline, resource-limited container.
  Receipts retain exact revisions, inputs, output and uncertain/interrupted states.
  The desktop provides setup, frozen-input authorization, readback and recovery.
- Optional local Playwright capture records URL, viewport and selected-element
  context without overwriting evidence. Publishing, completion, inspection,
  verification and contextual feedback have JSON CLI contracts.
- Publication/completion and guarded feedback contracts are covered for Claude,
  Codex and OpenCode. Live conversation validation remains outstanding: the rich
  transcript feed stays Claude-only until real Codex/OpenCode captures validate
  their readers and interaction gates. No unmeasured capability was enabled.
- Current code diffs explicitly represent the live checkout, not a frozen package
  diff. Existing local-check provenance and merge policy are unchanged.
- Initial storage limits: 64 KiB manifest; four screenshot pairs; PNG/JPEG only;
  4 MiB and 20 megapixels per image; 20 packages or 128 MiB per session, shared by
  its stack members. Reaching a limit refuses publication rather than deleting
  earlier review evidence. A retention-management UI is not included yet.
- Initial-release validation and its historical setup-test failure are recorded in
  [the original evaluation](evaluation/pr-review-workspace/README.md). Current
  extension passes build/vet and focused checks; the full suite still reports the
  unchanged TUI setup assertion. Checks and captures are recorded in
  [verification evidence](../.impeccable/review/review-verification/README.md).
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

## Reviewing a correction

Use **Mark reviewed** to remember the package you inspected. This records an
inspection only; it does not send feedback, approve a PR, run checks, or permit
merging. The selected version remains stable when another package arrives.

Choose the newer package, then **Since review** to compare its write-up sections
and screenshots against your recorded review. New, removed, and updated sections
remain visible. Identical screenshot bytes are not presented as a fresh capture.
**Compare code revisions** uses the two retained head hashes, excluding live
uncommitted changes. The original checkout and commits must still exist; retained
material comparisons continue working after cleanup. A base change is called out
separately because a head-to-head comparison alone is not the full PR diff.

The review baseline is scoped to each stack member, including retained packages
from ended stacks. Concurrent stale writes fail with a refresh instruction.
Changing versions does not implicitly mark anything reviewed. The daemon stores
the last inspection beside the immutable packages, separately from their content.

For prose feedback, choose **Comment section** before adding the comment. The
batch carries the section title and position together with the immutable package
and revision identities. Sending remains an explicit action.

The socket operations are `reviewPackageMark` (package ID and expected previous
review ID) and `reviewPackageDiff` (two package IDs). Both stay on the local human
transport; agent publication and the authenticated sandbox bridge cannot call them.
Those operations do not execute commands. Independent verification has a separate,
explicitly authorized local-human command described below; capture stays CLI-only.

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


## Independent revision checks

In **Review PR → Independent verification**, set up a check with a command, locally
available image pinned by SHA-256 digest, timeout and memory limit. **Review check**
freezes those inputs; **Authorize and run** starts that exact request. The image
must already contain its tools and dependencies: checks have no network access.

The runner clones the selected package's exact committed head into a private
workspace, excluding dirty working files. It reuses the goal runner's offline
container boundary, resource limits and cleanup receipts without granting goal
or merge authority. It does not mount host credentials or the Docker socket.
The source checkout is not modified. Checks currently require the original host
checkout; goal-managed and sandbox sessions retain their existing isolated
verification workflows. Previously recorded results remain readable after cleanup.

Each result carries package/base/head, check inputs, timestamps, exit outcome and
up to 16 KiB of output. Stale or unknown package freshness remains visible; a
passed result is not transferable to a newer head and does not raise the package's
confidence score or authorize merge. There are at most two active review checks
per daemon and 32 retained checks per package. Interrupted checks must be recovered
before another check starts for that package; recovery settles only the named
container and never silently reruns a command or labels interrupted work passed.

The UI retains a request ID across uncertain writes and requires successful
readback before unlocking another submission. Repeating an existing ID with the
same inputs reads its receipt; changing inputs under that ID is rejected.

## Optional browser capture

From the checkout, with Node and local `playwright` or `playwright-core` available:

```sh
lola review-package capture http://localhost:3000/settings \
  --output .lola/review/settings-after.png --title Settings \
  --width 1440 --height 900 --selector 'main' --channel chrome
```

Omit `--selector` for the visible page viewport. Omit `--channel` to use an already
installed Playwright Chromium; `chrome` and `msedge` select an installed browser.
Nothing downloads a browser or dependency automatically. The bounded capture uses
a fresh context without saved authentication, waits for loading/fonts, and returns
a JSON screen entry for the manifest. A missing browser or selector fails clearly.
Existing paths are never overwritten; capture output stays inside the checkout.

The screen entry records the final URL, requested viewport and optional selector.
The desktop shows that context and includes it in screenshot feedback. Capture is
an explicit local CLI operation, not an embedded browser or daemon execution API.
It proves neither the commit behind a running server nor a baseline: retain real
before images before editing, and associate only accurate revisions in the manifest.

## Automation and completion

Agent sessions use the same publication and completion contract for Claude, Codex
and OpenCode, including the authenticated sandbox bridge:

```sh
lola review-package publish .lola/review/manifest.json
lola review-package complete .lola/review/manifest.json
# Stack: retain completion material for this repository only.
lola review-package complete .lola/review/web.json --app web
```

Completion retains the package first and then records the ordinary session's
agent-reported phase as done. A stack member shares its session's progress board,
so member completion retains its material without marking the whole task done.
Completion is a claim; it never changes lifecycle axes, prompt gates, approval,
verification or merge authority. Identical material returns the original package.

Human automation uses the local socket and prints JSON receipts. Use the exact
member session ID (`<session>~<member>`) for stack material:

```sh
lola review-package list SESSION
lola review-package mark SESSION PACKAGE --previous PREVIOUS_PACKAGE
lola review-package feedback SESSION PACKAGE --file feedback.md --section Settings
lola review-package verify SESSION PACKAGE --request-id 0123456789abcdef0123456789abcdef \
  --name tests --command 'npm run test:unit' \
  --image 'sha256:REPLACE_WITH_LOCAL_IMAGE_DIGEST' --timeout 300 --memory 2048
lola review-package checks SESSION PACKAGE
lola review-package recover SESSION PACKAGE REQUEST_ID
```

Omit `--previous` for the first inspection. Generate a fresh 32-character lowercase
hex request ID for each newly authorized check; reuse it only for an unchanged
request whose response was uncertain. Commands make one request and do not retry
writes automatically. Feedback is bound to retained repository/PR/base/head and
section context and goes through the existing idle-gated queue. Feedback sends
are not idempotent: do not blindly retry after a lost response. Agent bridge and
remote peers cannot invoke these human operations.

Acknowledgment controls beyond explicit inspection, schedules, and remote execution
remain conditional future work under the plan's “as usage justifies” scope. This
extension does not enable them or add a retention-management UI. Native packaged
WKWebView and live cross-agent conversation remain validation gaps, separately
from the tested CLI/daemon contracts.
