# Nori dialog pilot: prepared baseline

Prepared on 2026-10-22 (Vienna). **No autonomous model run or live Linear
publication has started.** This is setup and acceptance evidence, not an outcome
comparison or proof of self-improvement. The user's execution ceiling remains
30 minutes, with an initial limit of 8 total agent calls and one worker at a time.

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
