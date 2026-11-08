# Independent review verification and capture

Evidence collected 2026-11-08 for the extension in
[the PR review workspace plan](../../../docs/pr-review-workspace.md).

## Browser evidence

These are headless WebKit captures of the real Svelte components in an isolated
Vite fixture. They do not show a live daemon or native packaged WKWebView.
The fixture's review material and verification receipt are synthetic; the receipt
explicitly says no real check was run. Existing Nori image fixtures provide the
surrounding review context. No synthetic operating-system input was used.

- [Setup at 1440px](form-1440.png) and [420px](form-420.png).
- Frozen authorization at [1440px](authorization-1440.png),
  [700px](authorization-700.png), and [420px](authorization-420.png).
- Retained receipt at [1440px](receipt-1440.png) and [420px](receipt-420.png).

The final captures use a 128-character unbroken check name. Browser assertions
verified no document/dialog horizontal overflow at all three widths, focus on
setup/review transitions, and no page errors. Component tests cover focus after
editing, closing, and submission, uncertain responses/readback, same-ID retry,
and stale selections. The finish reviewer's limited final verdict was **ship**:
both its focus-transition and long-name findings were resolved. The documenter
confirmed this extends the incumbent system; no design-system files changed.
The preexisting sidecar/document drift was left intact.

A separate real CLI invocation captured the fixture's Independent verification
region using installed Chrome, `--selector`, a 1440 × 1000 viewport and a new output
path. Its printed manifest entry correctly retained final URL, viewport, selector
and title. The optional command did not install a browser or dependencies.

## Execution and contract evidence

- Frontend full suite: 135 files / 1,898 tests passed before the final focus
  regression was added. Final focused suite: 52 tests passed; Svelte check had
  zero errors/warnings and the production build passed.
- Focused Go suites cover exact committed inputs despite dirty source files,
  container flags, source preservation, bounded output, replay identity,
  conflicting inputs, failure/error distinction, restart recovery, retained reads,
  human/agent/remote boundaries and CLI single-request behavior.
- Completion/publication and queued feedback were exercised for the Claude,
  Codex and OpenCode session kinds. A stack member's completion retains its
  own package without marking the shared progress board done.
- Race checks cover the verifier, capture helper and daemon review handlers.
- A real Docker integration check used an already-installed Alpine image with
  digest `sha256:171e65262c8018f8f09a360006af2acbb2c0c494cb2be933025b466ea3d672dc`.
  It verified committed input, absence of host tokens/Docker socket, read-only
  container root, preservation of dirty source files, and timeout cleanup.
  This test passes only when explicitly enabled by `LOLA_REVIEW_CHECK_IMAGE`;
  ordinary test runs use injected execution and never invoke Docker.

Commands:

```sh
make check
cd desktop/frontend
npm run check
npm test
npm run build
```

`make check` passed formatting, build and vet. Its daemon suite (228 seconds),
desktop suite, all review packages and other packages passed except the existing
`TestSetupGeneralSettingsAreOptionalAndEditable` assertion at `internal/tui/setup_test.go:136`
("agent/theme choices did not change"). The same failure reproduced from an isolated archive of untouched commit
`80fec70`. No TUI files were changed by this extension.

The full Go suite needs local test-socket access: the initial sandboxed run
failed Unix socket binds and was stopped, then repeated with that access.

## Remaining empirical coverage

Native packaged WKWebView and live coding-agent sessions were not exercised.
CLI and daemon contract parity does not establish transcript-reader or interactive
composer parity: the rich feed remains Claude-only. These changes were built
locally, without installing the binary or restarting the running daemon.
