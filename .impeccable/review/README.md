# Goal controls preview

These screenshots show the actual ProjectGoals component inside ProjectDetail,
rendered with local fixture data. They do not show live Linear issues, current
repository progress or a running autonomous goal.

- `goals-desktop.png`: project goal overview at 1280 × 900.
- `goals-narrow.png`: the same section at 500 × 900, without horizontal overflow.
- `goals-action.png`: complete pause review, including revision, reason and controls.

The scoped finish review cleared the goal section after fixing selection retention
and recapturing the complete action. It did not certify the full implementation
plan or exercise a live daemon. The section inherits Lola's existing tokens and
shared Button/Select components; no visual identity change was made.

## Execution settings preview

`execution-desktop.jpg` (1280 × 900) and `execution-narrow.jpg` (500 × 900)
show the actual ProjectForm Execution tab with local fixture data. They do not
demonstrate live configuration changes or authorize goal execution.

The documentation check compared ProjectForm with the incumbent app.css tokens
and shared Select, Checkbox and Button components. The tab retains the existing
modal, section hierarchy, responsive form rows and semantic colors; both captures
show complete controls without horizontal clipping. Source inspection confirms
that projects and stacks expose Execution while members do not. Goal availability
is explicitly separate from plan review, limits and execution authorization;
Docker, image and Linux Lola binary prerequisites are explained, with setup
directed to README.md, “The container sandbox”.

This is an ordinary extension of the existing system. No PRODUCT.md, DESIGN.md
or design sidecar was created. Previously noted motion-comment drift between
app.css and Button remains unrelated and unchanged. These captures and this
check do not certify the full autonomous-goal implementation or live behavior.

## Specialist settings preview

`specialists-desktop.jpg` (1280 × 900) and `specialists-narrow.jpg` (500 × 900)
show the actual SettingsForm Review surface with a synthetic API and fixture
data. No live settings were written and the installed app was not changed.

The documentation check inspected ReviewProfiles.svelte, its SettingsForm
integration and responsive rules, and the shared Disclosure, Button, Checkbox
and Select patterns against the existing app.css system. Specialist settings
reuse semantic colors, compact typography, bordered inputs and progressive
disclosure. The desktop form preserves paired labels and controls; the narrow
capture stacks fields and wraps specialist actions within the modal. Longer
content remains in the existing scroll region above the fixed Save/Cancel footer.

Source inspection confirms separate specialty and provider choices, disabled new
profiles, explicit provider availability guidance, and distinct ordinary-session
triggers versus accepted goal execution policy. The scoped reviewer recommended
shipping without fixes and the detector returned no findings. These are local
interface checks, not evidence of live review execution or saved configuration.

This ordinary Operate extension adds no visual system rules. No PRODUCT.md,
DESIGN.md or sidecar was created; unrelated preexisting system drift remains
reported above and unchanged.

## Goal planner preview

`planner-desktop.jpg` (1280 × 900) and `planner-narrow.jpg` (500 × 1000)
show the brief form in the actual GoalPlanner component within ProjectGoals.
`planner-review.jpg` (1280 × 1000) shows a separately selected saved proposal.
All three use a fake API: the form inputs and fixed saved proposal demonstrate
separate interface states, not a generated before/after result. No paid model
calls, real writes or installed-app changes were made for these captures.

The documentation check inspected GoalPlanner.svelte and its ProjectGoals
integration, comparing them with the incumbent app.css tokens and shared Button,
Select, Checkbox and Disclosure patterns. The captures retain the existing
bordered section, compact form fields, semantic colors and progressive disclosure.
Both brief captures show the request action without horizontal clipping; the
review capture shows task scope, open questions and the acknowledgment-gated
acceptance action. Source inspection confirms that proposal requests, refreshing
a saved result, local acceptance, publishing and execution authorization remain
distinct operations.

The scoped review found a saved-selection change handler reading the previous
bound value. It now passes the event's selected value directly to inspection.
The reported six passing tests include exact selection of two proposal IDs and
hiding/reopening during a busy operation. The subsequent scoped verdict was ship;
the single detector pass returned no findings. This evidence does not certify
live planning, billing, publication or autonomous execution.

No PRODUCT.md, DESIGN.md or sidecar was created. The custom planner toggle's use
of decorative accent as text and the full-record block's direct text-xs use are
local departures from app.css guidance, not new system rules; this documentation
pass records them without changing the implementation or unrelated prior drift.

## Authorization and publication preview

The actual GoalAuthorization component within ProjectGoals is captured in
`authorization-desktop.jpg` (1280 × 1753, full page),
`authorization-narrow.jpg` (500 × 2225), and `authorization-review.jpg`
(500 × 1077). `publication-review.jpg` (500 × 667) shows the separate Linear
publication confirmation in ProjectGoals.

These are declared synthetic fixtures with side effects disabled: zero task rows,
a fake image digest and a reused parent issue link. They demonstrate interface
states and do not represent a publishable domain fixture. No live publication,
authorization, paid agent calls or installed-app changes were performed.

The documentation check inspected GoalAuthorization.svelte, ProjectGoals.svelte
and all four captures against the existing token and shared component system.
The form preserves bordered fields, semantic colors, compact typography and
progressive disclosure. Paired desktop fields stack at narrow widths, and the
review displays scope, checks, specialist selections and tabular limits before an
explicit acknowledgment. Publication separately names the reviewed revision,
resource consequences and task count; its copy states that it does not authorize
execution. Source inspection confirms revision-bearing publication and submission
of a captured authorization policy rather than the editable form state.

The scoped reviewer recommended shipping without fixes, and the single detector
pass returned no findings. The implementation handoff reports passing frontend
checks, tests and build, plus focused daemon tests for read-only options and exact
revision publication. `make check` subsequently passed, including the full Go suite; the final
focused desktop start/publication bridge test also passed. These results do not constitute an end-to-end execution or publication
test.

This ordinary extension preserves the incumbent system. No PRODUCT.md, DESIGN.md
or sidecar was created, and previously recorded unrelated drift was not repaired
or promoted into system rules.

## TUI specialist settings preview

The ten `specialists-tui-{wide,compact}-{empty,picker,profile,error,remove}.png`
captures show the actual Go settings modal at 120 × 40 and 80 × 24 terminal
cells. The optional `settingsprofiles_capture_test.go` fixture produces ANSI
output from the modal and keyboard paths; xterm renders that output for capture.
Each image is labeled as an offline fixture with no live daemon. The remove
state shows the focused draft-removal action, not a saved deletion. Initial blank
captures caused by render timing were replaced before the scoped review.

The documentation check inspected `settingsprofiles.go`, its `settingsform.go`
integration and fixture tests, and sampled the wide profile and compact error
captures. The implementation preserves the incumbent keyboard settings modal,
palette, flat fields and indented subsections. Separate Specialists and Providers
headings distinguish focused review profiles from their execution integrations.
The compact form scrolls its fields while retaining contextual help, wrapped
validation errors and save/cancel navigation. New profiles start disabled; provider
availability remains explicit. Add, edit and remove operate on the settings
draft, with whole-form validation and atomic save/cancel behavior.

The scoped reviewer inspected all ten valid captures and recommended shipping
without fixes. A web detector was not run: this is a Go TUI, with xterm used only
to render the captured terminal output. The implementation handoff reports that
all TUI tests and `make check` passed (build, vet and the full Go suite; many
unchanged package results were cached). Tests cover profile round trips,
add/remove/cancel, rejected saves, project and rubric editing, asynchronous label
loading, compact navigation/errors, save failure and legacy migration. This
evidence does not establish a live pilot or live specialist execution; the binary
was not installed and the daemon was not restarted for this increment.

This is a narrow extension of the existing TUI system. No PRODUCT.md, DESIGN.md
or design sidecar was created, no new visual system rules were introduced, and
previously recorded unrelated drift remains unchanged.

## TUI goal planning and publication preview

The sixteen `planner-tui-{wide,compact}-{brief,editing,request,review,tasks,uncertain,saved,publication}.png`
captures show the actual Go TUI at 120 × 40 and 80 × 24 terminal cells.
`goalplanner_capture_test.go` renders offline fixtures to ANSI; xterm supplies
only the screenshot transport. All sixteen captures were opened and validated
during the implementation pass. This documentation pass sampled the wide brief,
compact active editor and compact publication confirmation, alongside
`goalplanner.go`, `goals.go`, `app.go` and their tests.

The extension reuses the incumbent palette, keyboard navigation, settings field
renderer and line editor. Compact views wrap and scroll content while retaining
contextual controls. The workflow distinguishes a bounded paid proposal request,
read-only recovery by durable request ID, acknowledgment and exact-revision
acceptance as a local goal, and a separate reviewed Linear publication action.
Uncertain results require refreshing the saved record before another acceptance;
refreshing does not repeat the model call. Publication names its resource effects
and reviewed revision. This increment adds no TUI execution authorization flow;
authorization remains available through the desktop and CLI.

The scoped reviewer initially found that root Ctrl+C handling bypassed unsent
draft protection and that multiline editing showed incorrect keyboard hints.
Both were fixed, active-editor captures were added, and
`TestGoalPlannerCtrlCProtectsUnsentBriefAndLineEditing` exercises the root Update
path. The final reviewer verdict was ship, with no outstanding material fixes.
The implementation handoff reports all TUI tests passing uncached with capture
fixtures enabled, followed by `make check` passing build, vet and the full Go
suite; unchanged package results were cached. No web detector applies to this Go
TUI surface.

These fixtures and checks do not establish live planning, billing, publication
or autonomous execution. No live daemon, paid model call, Linear write or binary
installation was used for this increment. This narrow extension preserves the
existing visual system: no PRODUCT.md, DESIGN.md or sidecar was created, and
previously recorded unrelated drift remains unchanged.

## TUI execution authorization preview

The sixteen `authorization-tui-{wide,compact}-{empty,editing,profiles,limits,error,review,review-end,uncertain}.png`
captures show the actual Go TUI at 120 × 40 and 80 × 24 terminal cells.
`goalauthorization_capture_test.go` renders offline fixtures to ANSI; xterm is
only the screenshot transport. The builder and fresh reviewer opened all sixteen
captures. This documentation pass sampled the wide empty form, compact command
editor, compact end of review and compact uncertain result, and inspected
`goalauthorization.go`, its `app.go`/`goals.go` integration and tests.

This increment adds execution authorization to the TUI after the planning and
publication increment documented above. It preserves the incumbent palette,
keyboard navigation, flat settings fields, indented sections and line editor.
Compact views wrap and scroll content while keeping contextual controls visible.
Read-only options supply repository identities, specialist availability and
suggested limits. The form edits path restrictions, operator-supplied acceptance
checks with commands, pinned image digests, timeouts and captured evidence,
specialist selections, and execution limits. The review
shows a snapshot of the policy and goal revision before explicit acknowledgment
of scope, checks, limits and paid agent calls. Token and spend limits are labeled
as delayed observations rather than exact billing caps.

Source and test inspection confirms that `goalStart` submits the reviewed
revision and frozen policy, guards pending requests and stale replies, and
protects the draft through Ctrl+C and discard handling. An uncertain start reply
blocks repeat authorization until the user closes the screen and refreshes Goals.
Tests also cover validation, check/evidence editing, mandatory specialists,
returning from review, and compact viewport contents.

The fresh reviewer recommended shipping without material fixes. The
implementation handoff reports the full TUI suite passing uncached after label
and heading corrections, followed by final `make check` passing build, vet and
the full Go suite. No web detector applies to this Go TUI surface.

These offline fixtures and checks do not establish live authorization, billing
or autonomous execution. No live daemon, paid model call, Linear write,
execution or binary installation was used for this increment. This ordinary
extension adds no visual system rules: no PRODUCT.md, DESIGN.md or sidecar was
created, and previously recorded unrelated drift remains unchanged.
