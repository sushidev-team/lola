# Nori dialog pilot: baseline and live attempts

Updated on 2026-10-22 (Vienna). Live attempts have exercised publication, worker
launch, submission, independent check rejection, restart and bounded stopping.
**The pilot has not completed specialist review, integration or PR delivery.**
The user authorized 30 minutes and eight model calls in aggregate, with one
worker at a time; the continuation retained those time/call ceilings.
The latest approval adds 20 minutes, an eight-minute worker attempt and up to
three further calls, retaining the 2M-token / $3 aggregate ceilings. See the
[current handoff](../../../HANDOFF-workspace-goals.md) for the authoritative
remaining allowance and device setup.

## Scope and baseline

The source is `sushidev-team/nori-app` at
`97fc399c591d82cfcf54859fa0c0191311cb16b9` on `main`. The original checkout
was clean before and after preparation. All setup, builds and browser checks
used a disposable archive under `/tmp/lola-nori-dialog-pilot`.

[The draft brief](brief.json) bounds work to the shared dialog presentation,
its stories/tests, and the invoice Save view dialog only if layout requires it.
Existing Nori tokens define the intended scrim and elevation. The captured
baseline actually renders an 80% black scrim in both themes and the older
two-layer shadow; [the interaction record](baseline/interaction.json) retains
actual and expected computed styles for all four theme/viewport combinations.

| Baseline check | Observed result |
| --- | --- |
| Existing dialog and invoice Save view unit tests | 10 passed |
| Existing dialog Storybook tests in Chromium | 3 passed |
| `npm run build` (TypeScript + Vite) | Exit 0; existing font-resolution and chunk-size warnings retained |
| Light/dark, 1280×900 and 390×844 browser checks | 4 passed |
| Initial focus, Tab containment, Escape and focus return | Passed in all four cases |
| Viewport gutters, vertical overflow and close-button visibility | Passed in all four cases |
| Token-style gate against unchanged baseline | Expected failure: actual scrim differs from the existing token |
| Lola's real Docker validation boundary and timeout cleanup | Passed with the final image |

The browser harness injects explicitly marked long content into the rendered
dialog to exercise scrolling. It does not claim that this is an existing
production form. Invoice payload preservation is covered by the existing unit
tests. Screenshots show the existing shared dialog **With form** story, not the
authenticated invoice page.

Baseline captures: [light desktop](baseline/light-desktop.png),
[light narrow](baseline/light-narrow.png),
[dark desktop](baseline/dark-desktop.png),
[dark narrow](baseline/dark-narrow.png). Corresponding `*-overflow.png` captures
retain the synthetic long-content state. Logs and measurements are in
[`baseline/`](baseline/); [hashes](baseline-sha256.json) bind these artifacts.
Terminal escape sequences and trailing blank lines were stripped from retained
text logs only.

## Reproducible acceptance environment

The locally installed image is
`sha256:d3f85088fc383f8114b9ed1e57ba8916a0c92d75fba8c914d3a7085da0d7d6c8`.
The [Dockerfile](Dockerfile) pins the Node base digest and Claude CLI package
`2.1.307`. Project dependencies come from Nori's committed lockfile, including
Playwright `1.59.1` and Storybook `10.3.6`. Chromium is installed at image build
time. [Input hashes](image-inputs.json) retain the package manifests, selected
vendor frontend sources, harness scripts and the built Lola binaries.

To reconstruct the build context, use `package.json` and `package-lock.json`
from the source commit, the files listed in `inputs_sha256` from the matching
vendor installation, and the four harness/Dockerfile files here. Vendor lock
references are Okane `v3.2.1` (`849df4a0a18d8ec362358a6c2a518fe504aa7f59`),
Ziggy `v2.6.4` (`c32a3eaad1574df8b953281680bba89bd3217dc2`) and Laravel
`v13.30.1` (`718d17db56861e0a49f644217c8853dab1bff8ce`). No private vendor
source or credential is vendored into this Lola documentation directory.
Rebuilding may produce a different image ID; pin the actual new ID and verify
it before authorizing execution.

Run `sh /opt/pilot/setup-deps.sh` from the disposable repository to prepare its
offline dependencies. `PILOT_ASSERT_STYLE=1 sh /opt/pilot/check.sh` is the proposed
acceptance command. It runs the existing tests/build and the independent browser
harness with finite command timeouts, retaining logs and screenshots under
`pilot-evidence/`. The acceptance container itself must retain Lola's network-off,
read-only-root, capability, resource and disposable-workspace controls.

Setup findings that changed the harness:

- Linking all of `node_modules` into the read-only image prevented Vite's
  temporary config cache. Linking individual packages then made Storybook's
  setup imports resolve outside Vite's normal allowed tree. Copying dependencies
  into the disposable workspace fixed both without changing Nori's config.
- Storybook's standalone preview needs `LARAVEL_BYPASS_ENV_CHECK=1` under CI;
  this is confined to the local preview process in the network-disabled check.
- That preview does not serve Laravel's public fonts, and its BlockNote Inter
  URLs contain an invalid `/undefined/` prefix. The harness fulfills only exact
  local font assets from the snapshot/dependencies. It verifies fonts are loaded
  before capture. It does not replace application CSS or accept silent fallback.
- Visible shadow comparisons ignore transparent Tailwind shadow layers; they
  still require the visible elevation to match the existing token.

## Live pilot stopped at its token budget

The user supplied a model credential through a local mode-600 file. Only the
model credential enters the isolated worker. Host GitHub/Linear credentials and
home directories are not mounted. Ordinary polling remains disabled and the
normal installed Lola daemon has not been changed.

The pilot began at 2026-10-21 23:52:48 UTC (2026-10-22 local time). Its ceiling
is 30 minutes including planning and eight model calls. One coordinator
proposed two tasks; the reviewed edit combined them into one implementation
and verification task without another model call. The accepted execution policy
retains the remaining 25 minutes, one worker, two attempts, eight total calls
including the coordinator, and a required UI/UX review. See
[authorization](authorization.json), [reviewed edit](proposal-edit.json), and
[live observations](live-run.json).

Linear contains [parent NOR-493](https://linear.app/sushi-dev/issue/NOR-493) and
[task NOR-494](https://linear.app/sushi-dev/issue/NOR-494) in the existing Nori
project, under the new **Dialog layout pilot** milestone. The task has the
existing `apps/app` label and no `agent-ready` label. Existing release milestones
and initiatives are unchanged.

The team workflow UI was inspected without changing it. Draft PR open has no
action; ordinary PR open moves an issue to In Progress; review activity moves it
to In Review; merge moves it to Done. No branch-specific rows were shown.
Automatic parent/subissue closing is off. Lola's goal execution integrates task
bundles locally, so there are no intermediate task PR merges; final publication
creates draft PRs and final merging stays human-controlled.

Actual worker base is `f94d30d7c8f08178c14297d67756a014d3ff089c`. Compared with the
prepared baseline, only CHANGELOG.md and the release manifest changed through
an upstream release merge. Dialog source, design rules and dependency inputs
are unchanged.

Live-discovered fixes:

- Linear serializes Markdown lists with blank lines and `*` bullets and removes
  the trailing newline. Lola now generates that form, retaining exact checks for
  human changes. Repository paths are code spans so `__tests__` is preserved.
  Publication resumed with the same reserved IDs. Only the newly created task's
  damaged path formatting was repaired in Linear.
- The first worker stopped at Claude's workspace trust prompt. The authorized
  private checkout was confirmed manually. Future goal launches now seed trust
  for only their authorized root, matching ordinary sandbox startup; existing
  state is preserved. This startup change has a focused regression test.

The active-worker restart test reused the same attempt and two existing calls
without a duplicate launch. [Restart evidence](restart-evidence.json) retains
both snapshots' identities. The pilot then stopped automatically at 285,000 observed tokens against its
250,000-token policy, including cached input, after two model calls (estimated
$0.315; partial telemetry). The operator-selected token limit was too low for
this context even though the time and call budgets remained available. No
accepted policy was rewritten.

The worker produced a two-line token-class change and passed 10 unit tests,
three Storybook tests, the build and all four browser cases including the style
gate. [Retained worker output](stopped-worker/) includes its patch, logs and
captures, bound by [hashes](stopped-worker-sha256.json). These are worker checks,
not independent goal-verification receipts. No submission, specialist review,
integration or draft PR occurred. The original checkout was not edited.

The stop also exposed an unfinished run timestamp after confirmed worker
termination. Lola now records the matching run finish time when termination is
acknowledged, with regression coverage. The historical pilot receipt remains
unchanged.

A fresh run with a cache-aware limit needs a new authorization; the user was
asked about a 2M-token / $3 ceiling while preserving the aggregate 30-minute /
eight-call limit. This is not yet a completed pilot or evidence of a quality
improvement.
Representative matched-budget comparisons remain a separate unfinished requirement.

All three Lola fixes pass focused regressions and the final `make check`. The
[retained check log](lola-check-final-fixes.log) records the full run. The normal
installed daemon remains untouched. This checkpoint retains the implementation
and verification evidence on the feature branch; the live pilot is incomplete.


## Authorized continuation and verification recovery

The user approved up to 20 additional minutes and six further model calls,
with aggregate ceilings of 2M observed tokens and $3 estimated spend. The earlier
run remained immutable. NOR-495 / NOR-497 reused the existing pilot milestone.
The committed trust fix allowed a prompt-free launch. The worker submitted
`71969919f32e6f9e84575b63557fdaa8b7fb3ebd`; Lola independently checked candidate
`66fa02bb78d85f71c15dda5f2689cc6339aa7df8` in a disposable, network-disabled
2 GiB container. Unit, Storybook and browser checks passed, but the build exited
134 at Node's default ~1 GiB heap limit. Lola rejected integration. The automatic
second worker was stopped to avoid spending budget on a check-environment problem.
[Continuation records](continuation/) retain the failure and candidate patch,
bound by [hashes](continuation-sha256.json).

Separate diagnostics kept the same 2 GiB isolation boundary: a 1536 MiB Node
heap still failed, while `NODE_OPTIONS=--max-old-space-size=1792` passed the build.
These diagnostics do not replace the failed independent receipt. A final recovery
(NOR-498 / NOR-499), within the remaining allowance, used that setting. Its worker
passed unit, Storybook, build and browser checks but reached its three-minute
attempt deadline before submission. That operator-selected attempt limit was too
short; it was not a code-check failure. [Recovery output](recovery/) and
[hashes](recovery-sha256.json) retain the result. No specialist review, goal
integration, draft PR or main-branch change occurred.

Observed aggregate usage is 1,224,631 tokens, including cache, five model calls
and approximately $0.806 estimated spend. Agent telemetry is partial; these are
observations, not a billing guarantee. The original Nori checkout remains clean
at `f94d30d7c8f08178c14297d67756a014d3ff089c`.

[Before/candidate screenshots](https://chatgpt.com/space/page_1288680f36c4819197328af1051006a8)
show the independently captured continuation candidate; the Page is private until
shared. They are Storybook evidence, not a successful delivery claim.

Lola now supports an explicit acceptance-check `memory_mib` authorization
(256–8192, default 2048), exposed in desktop/TUI and recorded in usage. Existing
policy hashes and limits are preserved. This implementation was not installed
into the running pilot, which used the existing 2 GiB limit throughout.

Before another autonomous attempt, preflight the complete acceptance command
under its exact container boundary and reserve enough time for worker submission,
independent checks and both review stages. Repeated new goals clutter the same
milestone: a future reviewed run-renewal operation should retain a stable goal and
Linear parent while appending a new immutable execution authorization. Do not
silently extend old budgets or overwrite failed receipts. Representative matched-
budget comparisons also remain unfinished.


Validation of the memory-limit implementation: `make check` passed (build, vet,
all Go tests); the focused TUI authorization suite passed after the final
assertion; six desktop authorization tests passed; Svelte check reported zero
errors/warnings; the production frontend build passed. The normal installed
binary was not replaced. The isolated pilot daemon was stopped after its workers
were confirmed stopped.

## Handoff-device preflight

The feature branch was fetched on the new device and matched `3aecea4`. Local
Nori and its remote main both remain at `f94d30d7c8f08178c14297d67756a014d3ff089c`;
all package, selected vendor and harness input hashes match. The rebuilt ARM64
image is `sha256:5a75b7519c908829a8151115e5f2a32c73de18e30150b5cfaa79fd47bea8d674`.
Lola host/Linux binaries were built and the missing tmux runtime was installed.

The exact command `NODE_OPTIONS=--max-old-space-size=1792 PILOT_ASSERT_STYLE=1 sh /opt/pilot/check.sh`
passed against a disposable archive with the historical recovery patch under
the network-disabled, read-only-root, 2 CPU, 2 GiB, 256-process boundary with
512 MiB scratch. It passed 11 unit tests, 3 Storybook tests, the build and all
four real-browser cases, taking 95.386 seconds. Existing font-resolution and
chunk-size build warnings remain. The actual Docker boundary and timeout-cleanup
test passed separately. [Logs, captures and measurements](handoff-preflight/)
are bound by [hashes](handoff-preflight/sha256.json).

These are environment preflight results for the historical candidate, not a new
worker submission, specialist verdict or integration proof. No new goal or
Linear issues were created, no daemon was started and no model calls were spent.
The three approved calls, 775,369 observed-token allowance, $2.1938334 estimated
spend allowance and additional 20-minute execution window remain available.
Execution awaits secure local Linear and Claude authentication. Prepared draft
and authorization files are retained here; the authorization deliberately keeps
its placeholder goal ID/revision and cannot start a run as-is. Original Nori
source and ordinary daemon state remain unchanged.
