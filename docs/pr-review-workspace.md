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
CLI sends content through the existing daemon transport, including the container
hook bridge where supported. All three coding agents use the same contract.

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

- Plan committed before implementation.
- Implementation and validation are pending; update this section with completed
  slices and concrete remaining work.
