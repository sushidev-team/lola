# Review tools refinement

2026-11-09. Requested changes: group the independent-verification text/form,
add UI browser capture, and contain the overflowing reported command.

## Implemented flow

- Verification has one compact revision header, related command/image inputs,
  optional name/resource settings, and the existing explicit authorization step.
- Capture is available beside the review tabs. Its inline form adds or updates
  a screenshot in a new immutable version and opens the saved image. It preserves
  old material and baselines, and checks checkout/known PR identity before and
  after capturing. It does not modify the agent checkout or attest to the live
  server's code revision. Node, Playwright and the selected browser must exist.
- Reported commands wrap inside their region, including unbroken text. Completion
  indicators remain aligned and visible.

## Render evidence

Real Svelte components, isolated Vite fixture, headless WebKit; no synthetic
operating-system input. These screenshots contain synthetic review data and an
explicitly labelled synthetic capture receipt, not production results.

- Verification: [1440](verification-1440.png), [700](verification-700.png),
  [420](verification-420.png), [options](verification-options-420.png),
  [authorization](authorization-420.png).
- Capture: [1440](capture-1440.png), [700](capture-700.png), [420](capture-420.png),
  [options](capture-options-420.png), [saved result](capture-result-1440.png).
- Local checks: [1440](local-checks-1440.png), [420](local-checks-420.png).

Browser assertions checked zero document/dialog horizontal overflow at all three
widths, no JavaScript errors, initial command focus, capture-to-screenshot
navigation, and the local-check container's bounds. The checks fixture includes
the command from the user's screenshot and a long unbroken stress case.
All files were visually inspected. The design detector returned no findings.
The fresh finish reviewer returned **ship** with no required corrections in scope.
The documenter confirmed this extends the existing system; no design-system files
were changed, and preexisting sidecar drift was left intact.

## Verification

- Svelte: zero errors/warnings.
- Full frontend suite: 137 files, 1,913 tests passed; production build passed.
- Focused Go race tests: daemon capture handlers, capture helper and committed-code
  reader passed. Tests cover retained old versions, baseline preservation, host-only
  execution, invalid inputs, stale revisions and revision movement during capture.
- Real Chrome integration: a temporary local HTTP page was captured through the
  new daemon handler, its image retained, and final URL/selector checked. This
  test uses the existing Playwright installation and is opt-in via
  `LOLA_REVIEW_CAPTURE_MODULES`; normal tests inject the browser process.
- `make check`: formatting, build, vet, desktop and daemon suites passed. The
  only failure is the existing `TestSetupGeneralSettingsAreOptionalAndEditable`
  assertion in `internal/tui/setup_test.go:136`, previously reproduced on untouched
  commit `80fec70`. No TUI files were changed by this task.
- Component tests cover exact capture inputs, saved-version navigation, stale
  responses, failure draft retention, and four-screen updates, alongside existing
  verification authorization/readback tests.

Native packaged WKWebView was not exercised. The local application has not been
installed or restarted by this task. Concurrent work on other features remains
outside this refinement.
