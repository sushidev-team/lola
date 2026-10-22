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

## Workspace learning preview

The ten `knowledge-{desktop,narrow}-{lesson,accept,review,uncertain,capture}.jpg`
captures show the actual ProjectKnowledge component with offline fixtures in
WebKit at 1100px and 500px widths. They are labeled as sample lessons with no
daemon or repository writes. The builder and fresh reviewer opened all ten;
this documentation pass sampled the desktop lesson and narrow capture, review
and uncertain-result states, and inspected ProjectKnowledge.svelte, its
ProjectDetail.svelte integration and component tests.

The section preserves the incumbent app.css palette and typography, bordered
fields, shared Button, Checkbox, Select and Disclosure components, and restrained
section hierarchy. Paired fields stack at narrow widths, long lesson content
wraps, and sampled captures retain complete actions without horizontal clipping.
The list exposes search, status and provenance before a separate decision form.
Candidate capture and explicit correction remain distinct; mandatory policy and
prevention changes are proposals rather than an implicit change to project rules.

Source and test inspection confirms version-scoped acceptance and dispute,
explicit conflict replacement, committed code references, and retention of an
existing expiry unless the operator clears it. A captured decision is reviewed
before acknowledgment and submission. Pending and uncertain results block repeat
submission; an uncertain outcome directs the operator to close and refresh the
saved lessons. Replies from a previous project are ignored. Export exposes a
read-only content preview for normal code review, without writing repository files.

The fresh reviewer recommended shipping with persistence, fidelity and ceiling
checks passing and no material fixes requested. The single detector result was
inspected and contains no findings. The implementation handoff reports passing
`make check`, a full frontend run of 65 files and 785 tests before the final small
fixes, and then nine focused tests, type checking with zero errors or warnings,
and a production build after those fixes. Backend bridge and curation tests are
reported to cover identities, scope, versions and avoiding retries. This
documentation pass did not rerun those checks.

These fixtures and checks do not establish a live curation pilot. No live daemon,
Linear write, paid model call, installation or restart was used for this increment.
This is an ordinary Operate extension of the incumbent interface, without an
approved comp or quality card. No PRODUCT.md, DESIGN.md or sidecar was created;
no visual system rules were added, and unrelated drift remains unchanged.

## TUI Workspace learning preview

The eighteen `knowledge-tui-{wide,compact}-{list,lesson,accept,review,review-end,uncertain,editing,capture,error}.png`
captures show the actual Go TUI at 120 × 40 and 80 × 24 terminal cells.
`TestCaptureKnowledge` in `knowledge_test.go` renders offline fixtures to ANSI;
xterm supplies only the screenshot transport. The builder and fresh reviewer
opened all eighteen captures. This documentation pass sampled the wide lesson
and compact active editor, end of review and uncertain-result states, and
inspected `knowledge.go`, its `app.go`/`detail.go`/`detail_view.go` integration
and tests against the incumbent settings and authorization forms.

The project action `K` opens Workspace learning. The surface retains the
existing vitals bar, highlighted breadcrumb, flat settings fields, indented
list editor and keyboard navigation. Compact content wraps and scrolls while
contextual help, acknowledgment and recovery controls remain visible. Search
leads to lesson details with source, verification, scope, expiry and evidence;
full provenance is separately available. Capture distinguishes a candidate from
an explicit correction, and labels policy and prevention changes as proposals.

Source and test inspection confirms that acceptance and dispute capture the
exact lesson version and scope for review before acknowledgment. Acceptance
retains an existing expiry unless explicitly cleared, requires retaining or
replacing tracked code references, and exposes conflict replacement choices.
The shared settings editor supports multiline paste and protects unsaved drafts
through Escape and Ctrl+C. Pending requests and uncertain results block repeat
submission; closing an uncertain result refreshes the saved lessons. Late replies
cannot replace another project's view. Export is a read-only content preview for
normal code review, without repository writes. Rendered content uses the existing
terminal sanitization path; viewport tests cover control sequences, bidirectional
text controls, width limits and persistent decision controls.

The fresh reviewer recommended shipping with persistence, fidelity and ceiling
checks passing and no material fixes requested. The implementation handoff
reports the full TUI suite passing uncached with capture fixtures enabled
(`/tmp/lola-tui-learning-test.log`), followed by final `make check` passing build,
vet and the full Go suite (`/tmp/lola-tui-learning-check-final.log`). This
documentation pass inspected tests but did not rerun them. No web detector
applies to this Go TUI surface.

These fixtures and checks do not establish a live curation pilot. No live daemon,
paid model call, Linear write or binary installation was used for this increment.
This is an ordinary Operate extension of the incumbent TUI, without an approved
comp or quality card. No PRODUCT.md, DESIGN.md or sidecar was created, no visual
system rules were added, and unrelated drift remains unchanged.

## Desktop task proposal editing preview

The ten `graph-{desktop,narrow}-{edit,scope,review,uncertain,history}.jpg`
captures show the actual GoalPlanner, GoalPlanEditor and GoalPlanTasks Svelte
components in an offline WebKit fixture at 1100px and 500px viewport widths.
Review and uncertain-result images are region crops of their respective
viewports. The evidence covers these separate interface states:

| State | Desktop | Narrow |
| --- | --- | --- |
| Task editing | [Edit](graph-desktop-edit.jpg) | [Edit](graph-narrow-edit.jpg) |
| Repository scope and routing | [Scope](graph-desktop-scope.jpg) | [Scope](graph-narrow-scope.jpg) |
| Frozen revision review | [Review](graph-desktop-review.jpg) | [Review](graph-narrow-review.jpg) |
| Unconfirmed save and refresh | [Uncertain](graph-desktop-uncertain.jpg) | [Uncertain](graph-narrow-uncertain.jpg) |
| Saved revision and provenance | [History](graph-desktop-history.jpg) | [History](graph-narrow-history.jpg) |

This documentation pass inspected the three components, `goalplanedit.ts` and
focused test assertions, and opened the desktop edit/history and narrow
scope/review/uncertain captures. The extension preserves the incumbent palette,
compact typography, bordered fields, shared Button and Checkbox controls, and
Disclosure for sources and the planning record. Native task disclosures keep
individual forms manageable. Sampled narrow captures wrap task descriptions,
repository paths and review controls without horizontal clipping.

Task changes use explicit forms for acceptance coverage, dependencies, repository
scope, writable paths, labels and open questions. Add, remove and move actions
preserve dependency references; removal is unavailable while another task depends
on the selected task, and a prerequisite cannot move after its dependent. The
user's original goal criteria remain fixed above the editor. Source inspection
confirms that review freezes the proposal identity, revision, edit ID, reason and
replacement output before acknowledgment. Saving a proposal revision and accepting
it as a local goal are separate actions; returning to the saved proposal clears
the acceptance acknowledgment. Original model output and revision history remain
available through the planning record.

An unconfirmed save retains the reviewed content and edit ID, removes the save
action, and requires a read-only refresh of the saved proposal before another
decision. Pending requests disable repeated submission. The review explains that
saving rechecks routing without a model call, issue creation or worker start;
acceptance, publication and execution authorization remain separate boundaries.

The fresh finish reviewer recommended shipping with persistence, fidelity and
ceiling checks passing and no material fixes requested. The automatic design
hook reported no deterministic issues. The implementation handoff reports a full
frontend suite passing with 67 files and 799 tests
(`/tmp/lola-graph-ui-suite.log`), 18 focused tests
(`/tmp/lola-graph-ui-tests.log`), type checking with zero errors or warnings
(`/tmp/lola-graph-ui-check.log`), a successful production build with the existing
bundle-size advisory (`/tmp/lola-graph-ui-build.log`), and passing `make check`
(`/tmp/lola-graph-ui-make-check.log`). This documentation pass inspected source
and screenshots; it did not rerun those checks.

These offline fixtures do not establish a live graph-editing pilot or completion
of the full self-improving workspace plan. No paid model call, live daemon or
Linear write, installation or live pilot was performed for this increment. The
fixture server was stopped and its temporary configuration removed. This ordinary
Operate extension has no approved comp or quality card and adds no visual system
rules. No PRODUCT.md, DESIGN.md or design sidecar was changed; previously recorded
unrelated drift remains unchanged.


## TUI task proposal editing preview

The eighteen `graph-tui-{wide,compact}-{graph,task,scope,editing,notes,review,review-end,uncertain,history}.png`
captures show the actual Go TUI at 120 × 40 and 80 × 24 terminal cells.
`TestCaptureGoalGraph` renders offline fixtures to ANSI; xterm and WebKit provide
the screenshot transport. The evidence covers these separate interface states:

| State | Wide | Compact |
| --- | --- | --- |
| Task graph draft | [Graph](graph-tui-wide-graph.png) | [Graph](graph-tui-compact-graph.png) |
| Individual task form | [Task](graph-tui-wide-task.png) | [Task](graph-tui-compact-task.png) |
| Repository and writable-path scope | [Scope](graph-tui-wide-scope.png) | [Scope](graph-tui-compact-scope.png) |
| Active line editor | [Editing](graph-tui-wide-editing.png) | [Editing](graph-tui-compact-editing.png) |
| Open questions and revision reason | [Notes](graph-tui-wide-notes.png) | [Notes](graph-tui-compact-notes.png) |
| Frozen revision review | [Review](graph-tui-wide-review.png) | [Review](graph-tui-compact-review.png) |
| End of review and acknowledgment | [Review end](graph-tui-wide-review-end.png) | [Review end](graph-tui-compact-review-end.png) |
| Unconfirmed save and refresh | [Uncertain](graph-tui-wide-uncertain.png) | [Uncertain](graph-tui-compact-uncertain.png) |
| Saved revision and history | [History](graph-tui-wide-history.png) | [History](graph-tui-compact-history.png) |

This documentation pass inspected `goalplanedit.go`, the request handling and
rendering in `goalplanner.go`, the planner paste route in `app.go`, and focused
assertions and capture setup in `goalplanedit_test.go` and
`goalplanedit_capture_test.go`. It opened the wide graph and compact active editor,
review-end and uncertain-result captures. The interface retains the incumbent
`settingsForm`, indented line editor, vitals bar, highlighted breadcrumb and
contextual footer. Sampled compact captures retain editing, acknowledgment and
recovery controls. Help and error text are bounded so long content cannot consume
the whole frame.

From a proposed plan, `e` opens a separate graph draft. Per-task forms expose
title, description, acceptance criteria, goal-criterion coverage, earlier-task
dependencies, repositories, writable paths and routing labels. `n` adds a task;
`x` asks for confirmation before removal; `u` and `d` move tasks while remapping
dependency references. Removing a referenced prerequisite or moving it after a
dependent is refused. `m` edits open questions and the revision reason. Goal
criteria remain fixed, and dirty form or graph exits require a discard decision.

Source inspection confirms that `r` validates and freezes an independent edit
request, including the proposal identity, revision and new EditID. Space
acknowledges that review before `s` saves it. Pending requests block repeat
submission, and a successful save must identify the reviewed edit. An unconfirmed
save preserves the frozen content and EditID; `f` reads the saved proposal without
resending the edit. Late replies are scoped by project, generation and request
sequence. Saving returns to the proposal with acknowledgment cleared, so accepting
it as a local goal requires a fresh decision. Original model output and revision
history remain available in the planning record. Saving a revision, accepting a
local goal, publishing issues and authorizing execution remain separate actions.

The fresh finish reviewer reports opening all eighteen captures and recommends
shipping, with persistence, fidelity and ceiling checks passing and no material
fixes requested. The implementation handoff reports ten new behavior and viewport
tests, an uncached full TUI suite passing
(`/tmp/lola-tui-graph-final-tests.log`), and final `make check` passing build, vet
and the full Go suite (`/tmp/lola-tui-graph-check-final.log`). This documentation
pass inspected source, test assertions and the sampled screenshots; it did not
rerun those checks. No web detector applies to this Go TUI surface.

These offline fixtures do not establish a live graph-editing pilot or completion
of the full self-improving workspace plan. No live daemon, Linear write, paid model
call, pilot or binary installation was used for this increment. This ordinary
Operate extension has no approved comp or quality card and adds no visual system
rules. No PRODUCT.md, DESIGN.md or design sidecar was changed; previously recorded
unrelated drift remains unchanged.


## Optional human delivery acceptance preview

These twenty-four offline fixture captures show the actual desktop and TUI
acceptance interfaces. The ten desktop images cover the GoalAuthorization and
ProjectGoals Svelte components at desktop and narrow widths; the fourteen TUI
images cover the actual Go rendering at 120 × 40 and 80 × 24 terminal cells.
`TestCaptureGoalAcceptance` supplies the TUI ANSI fixtures.

| Desktop state | Desktop | Narrow |
| --- | --- | --- |
| Authorization with separate acceptance | [Authorization](acceptance-desktop-authorization.jpg) | [Authorization](acceptance-narrow-authorization.jpg) |
| Delivered, awaiting acceptance | [Pending](acceptance-desktop-pending.jpg) | [Pending](acceptance-narrow-pending.jpg) |
| Reviewed delivery and evidence | [Review](acceptance-desktop-review.jpg) | [Review](acceptance-narrow-review.jpg) |
| Unconfirmed result and recovery | [Uncertain](acceptance-desktop-uncertain.jpg) | [Uncertain](acceptance-narrow-uncertain.jpg) |
| Saved acceptance receipt | [Accepted](acceptance-desktop-accepted.jpg) | [Accepted](acceptance-narrow-accepted.jpg) |

| TUI state | Wide | Compact |
| --- | --- | --- |
| Separate acceptance option | [Option](acceptance-tui-wide-option.png) | [Option](acceptance-tui-compact-option.png) |
| Frozen authorization review | [Authorization](acceptance-tui-wide-authorization.png) | [Authorization](acceptance-tui-compact-authorization.png) |
| Delivered, awaiting acceptance | [Pending](acceptance-tui-wide-pending.png) | [Pending](acceptance-tui-compact-pending.png) |
| Delivery review | [Review](acceptance-tui-wide-review.png) | [Review](acceptance-tui-compact-review.png) |
| Required evidence entered | [Evidence](acceptance-tui-wide-evidence.png) | [Evidence](acceptance-tui-compact-evidence.png) |
| Unconfirmed result and recovery | [Uncertain](acceptance-tui-wide-uncertain.png) | [Uncertain](acceptance-tui-compact-uncertain.png) |
| Saved acceptance receipt | [Accepted](acceptance-tui-wide-accepted.png) | [Accepted](acceptance-tui-compact-accepted.png) |

This documentation pass inspected the acceptance-related source and assertions in
`GoalAuthorization.svelte`, `ProjectGoals.svelte` and their tests,
`goalauthorization.go`, `goals.go`, `goalacceptance_test.go`,
`goalacceptance_capture_test.go`, and the goal evidence paste route in `app.go`.
It opened exactly four representative captures: desktop accepted, narrow
uncertain, TUI wide authorization and TUI compact evidence. The desktop retains
its incumbent dark palette, compact type, bordered fields, shared buttons and
checkboxes, and disclosures. The TUI retains its dark terminal ground, monospace
hierarchy, cyan breadcrumb, settings form and contextual footer. The sampled
narrow state wraps recovery text and evidence; the compact TUI keeps the reviewed
revision, criteria, delivered commit, evidence and submission help visible.

Separate human acceptance is optional in execution authorization and requires
`publish_pr`. Both authorization reviews carry the frozen policy choice. After
final deliveries are observed merged, the user reviews the delivered goal
revision and supplies nonblank acceptance evidence. This records acceptance;
it does not merge or deploy code. The desktop action retains its reviewed goal
while polling is suspended; the TUI captures the reviewed ID and revision in its
control request. Pending submission blocks repeat actions. An unconfirmed reply
preserves evidence, freezes editing and submission, and requires closing the
action and refreshing the saved goal before another decision. The saved receipt
shows the reviewed revision, timestamp and evidence, separately from the goal's
new current revision. TUI tests cover long review scrolling, evidence retention,
paste blocking after an uncertain result, and safe receipt text rendering.

The fresh finish reviewer reports opening all twenty-four captures and recommends
shipping with no material fixes requested: incumbent type, material, ground and
interactions match, and compact TUI content scrolls. This documentation pass
inspected the frontend logs: 67 files and 801 tests pass
(`/tmp/lola-acceptance-ui-suite.log`), type checking reports zero errors and zero
warnings (`/tmp/lola-acceptance-ui-check-final.log`), and the production build
passes with bundle-size and plugin-timing advisories
(`/tmp/lola-acceptance-ui-build.log`). The implementation handoff reports focused
Go/TUI/domain checks passing. At the time of this entry, full `make check` is
rerunning: a concurrent frontend build removed an embedded dist asset during the
first Go test compile. This entry does not claim a final full Go result, and this
documentation pass did not rerun checks.

These screenshots are evidence artifacts, not new raster product assets. No live
daemon, Linear write, paid agent call, live pilot or binary installation was used
for this increment; offline evidence does not establish a live acceptance pilot
or completion of the full self-improving workspace plan. This is an ordinary
Operate extension, with no new visual world or visual-system rules. PRODUCT.md,
DESIGN.md, the design sidecar and all existing review entries remain unchanged;
previously recorded unrelated drift is neither canonized nor repaired.

Final validation update: the implementation handoff confirms `make check`
completed successfully (exit 0), covering build, vet and the full Go suite in
`/tmp/lola-acceptance-ui-make-check-final.log`; this documentation pass inspected
the log tail. Temporary Unix socket bridge tests required sandbox escalation on
the rerun. This supersedes the pending full-check status above.


## Reviewed stack rollout authoring and display

These twenty-eight offline captures cover the actual `GoalAuthorization`,
`GoalRollout` and `ProjectGoals` desktop components in WebKit at 1100px and 500px,
and the actual Go `goalauthorization`, `goalrollout` and `goals` rendering through
ANSI/xterm fixtures at 120 × 40 and 80 × 24 cells. The domain and CLI already
carry the rollout context; this increment exposes its authoring and reviewed
readback in both interfaces.

| Desktop state | Desktop | Narrow |
| --- | --- | --- |
| Compatibility and complete member order | [Fields](rollout-desktop-fields.jpg) | [Fields](rollout-narrow-fields.jpg) |
| Invalid order | [Error](rollout-desktop-error.jpg) | [Error](rollout-narrow-error.jpg) |
| Frozen revision review | [Review](rollout-desktop-review.jpg) | [Review](rollout-narrow-review.jpg) |
| Unconfirmed authorization | [Uncertain](rollout-desktop-uncertain.jpg) | [Uncertain](rollout-narrow-uncertain.jpg) |
| Accepted policy in goal overview | [Saved](rollout-desktop-saved.jpg) | [Saved](rollout-narrow-saved.jpg) |
| Legacy goal without a contract | [Legacy](rollout-desktop-legacy.jpg) | [Legacy](rollout-narrow-legacy.jpg) |
| Validation summary focused after bottom submission | [Error focus](rollout-desktop-error-focus.jpg) | [Error focus](rollout-narrow-error-focus.jpg) |
| Unconfirmed result focused after bottom submission | [Uncertain focus](rollout-desktop-uncertain-focus.jpg) | [Uncertain focus](rollout-narrow-uncertain-focus.jpg) |

| TUI state | Wide | Compact |
| --- | --- | --- |
| Rollout fields | [Fields](rollout-tui-wide-fields.png) | [Fields](rollout-tui-compact-fields.png) |
| Compatibility editing | [Editing](rollout-tui-wide-editing.png) | [Editing](rollout-tui-compact-editing.png) |
| Frozen revision review | [Review](rollout-tui-wide-review.png) | [Review](rollout-tui-compact-review.png) |
| Unconfirmed authorization | [Uncertain](rollout-tui-wide-uncertain.png) | [Uncertain](rollout-tui-compact-uncertain.png) |
| Accepted policy in goal overview | [Saved](rollout-tui-wide-saved.png) | [Saved](rollout-tui-compact-saved.png) |
| Legacy goal without a contract | [Legacy](rollout-tui-wide-legacy.png) | [Legacy](rollout-tui-compact-legacy.png) |

Publishing a stack requires explicit compatibility requirements and a human merge
order containing every member ID exactly once. A single repository may omit both.
Disabling publication omits the contract from authorization while retaining the
form's drafts for re-enabling publication. Review freezes the selected goal
revision and policy before acknowledgment. An uncertain submission locks repeat
submission and directs the user to reload or reopen current state. The accepted
policy appears in the goal overview; it does not perform merges or deployment.
Legacy authorized stacks without a recorded contract explicitly say that repository
listing order is not a merge recommendation.

This documentation pass inspected the relevant desktop and TUI source and opened
four representative captures: desktop review, narrow error focus, TUI wide fields
and TUI compact legacy. Compared with the incumbent acceptance surfaces, the
desktop retains the dark ground, compact typography, bordered fields, shared
buttons and disclosure hierarchy. The TUI retains monospace hierarchy, cyan
breadcrumb, scrolling settings form and contextual footer. Existing components,
`style.css` and the acceptance captures remain the visual authority.

The fresh finish reviewer initially inspected all twenty-four original captures
and requested a fix: validation and uncertain-result errors could remain offscreen
when submitted from the bottom. The final verdict is **ship**, resolving that
specific finding through a focusable error summary with explicit
`scrollIntoView`. Four additional focus captures and WebKit active-element and
viewport assertions confirm that correction; uncertain submission remains locked.
This is a targeted resolution, not a new entire-surface review. The implementation
handoff reports focused checks passing after the correction; final full checks
were still in progress when this entry was written. This documentation pass did
not run tests or implementation.

These artifacts establish offline interface evidence only. No installed daemon,
external writes, live pilot or binary installation was used. PRODUCT.md,
DESIGN.md and `.impeccable/design.json` are absent and were not created. This is an
ordinary Operate extension with no approved visual-system change; all existing
review entries and system files are preserved.

Final rollout validation update: `make check` passed with the refreshed desktop
bundle (`/tmp/lola-rollout-ui-make-check-final.log`). The final frontend suite
passed 67 files / 804 tests; type checking reported zero errors and warnings,
and the production build passed. This supersedes the pending full-check status
in the rollout entry above. No live daemon or pilot was exercised.


## Goal recovery and Linear reconciliation preview

These sixteen offline fixture captures show the actual `GoalRecovery` and
`GoalBrief` desktop components at 1100px and 500px. They demonstrate local
inspection, explicit decision review, and recovery from an unconfirmed reply;
the fixture's changed revision is synthetic evidence, not a live Linear result.

| State | Desktop | Narrow |
| --- | --- | --- |
| Local recovery snapshot | [Inspect](recovery-desktop-inspect.jpg) | [Inspect](recovery-narrow-inspect.jpg) |
| Explicit clarified brief and decision reason | [Edit](recovery-desktop-edit.jpg) | [Edit](recovery-narrow-edit.jpg) |
| Frozen brief comparison and confirmation | [Review](recovery-desktop-review.jpg) | [Review](recovery-narrow-review.jpg) |
| Unconfirmed reply with replay locked | [Uncertain](recovery-desktop-uncertain.jpg) | [Uncertain](recovery-narrow-uncertain.jpg) |
| Reloaded accepted clarification | [Recovered](recovery-desktop-recovered.jpg) | [Recovered](recovery-narrow-recovered.jpg) |
| Retained progress update decision | [Progress](recovery-desktop-progress.jpg) | [Progress](recovery-narrow-progress.jpg) |
| Shared delivery evidence review | [Evidence](recovery-desktop-evidence.jpg) | [Evidence](recovery-narrow-evidence.jpg) |
| Verification retry review | [Retry](recovery-desktop-retry.jpg) | [Retry](recovery-narrow-retry.jpg) |

This documentation pass inspected `GoalRecovery.svelte`, `GoalBrief.svelte`,
the `ProjectGoals.svelte` integration, and `goalrecovery.ts`; it compared the
implementation with `GoalAuthorization.svelte` and the actual incumbent token
source, `desktop/frontend/src/app.css`. It opened three recovery captures:
desktop review, narrow uncertain and narrow recovered, plus the desktop rollout
review reference. The sampled views preserve the dark ground, compact utility
typography, semantic warning/error colors, bordered fields, shared buttons and
native disclosure hierarchy. The brief comparison is paired on desktop and
stacks at narrow width; source prose, reasons and identifiers wrap. No new
material visual drift was identified in this scoped documentation inspection.
Earlier unrelated drift recorded above is neither repaired nor adopted as policy.

Recovery opens a local snapshot and suspends the parent goal polling and competing
goal actions. Choosing an available action leads through editable fields to a
captured review and explicit confirmation. Brief reconciliation and progress
dismissal require reasons; retries allow optional reasons. The review retains the
goal revision and exact Linear source or progress update identity for the
corresponding mutation. Refreshing the Linear brief is separately identified as
an external read, while reloading the local snapshot does not contact Linear.
The copy distinguishes clarification from expanded task scope, progress dismissal
from confirmed remote delivery, an evidence URL from verification, and retries
from expanded permissions or budgets. An unconfirmed action keeps the review
visible, disables editing and repeat submission, focuses its error, and requires
a new local snapshot before another decision. Successful submission also reloads
local state before offering another decision. Source inspection supports these
behaviors; screenshots alone do not establish their live execution.

The fresh finish reviewer reports inspecting all sixteen recovery captures plus
rollout reference/source and recommends **ship**, with no material findings. The
single detector result is empty (`/tmp/lola-recovery-ui-detect.json`). This
pass inspected the frontend validation logs: 69 files and 814 tests pass
(`/tmp/lola-recovery-ui-alltests.log`), type checking reports zero errors and
warnings (`/tmp/lola-recovery-ui-types-final.log`), and the production build
passes with a bundle-size advisory (`/tmp/lola-recovery-ui-build-final.log`).
The implementation handoff confirms final `make check` completed with exit 0
(`/tmp/lola-recovery-ui-make-check-final.log`); this pass inspected its log tail.
That covers build, vet and the full Go suite, with platform linker advisories.
The documentation pass did not rerun checks.

This is offline interface evidence. No live daemon, Linear write, paid agent call
or live pilot was exercised, and this documentation pass did not independently
exercise keyboard navigation. The screenshots are review artifacts, not new
product imagery. This ordinary Operate extension creates no new visual world or
system rules. PRODUCT.md, DESIGN.md and `.impeccable/design.json` remain absent;
all prior review entries are preserved.


## Goal recovery and Linear reconciliation in the TUI

These twenty-two offline captures render the actual Go recovery fixture's ANSI
output in xterm at 120×40 and 80×24. They cover the local snapshot, editable
clarification, source inspection, frozen review, uncertain reply and recovered
history, plus progress, evidence and retry decisions. The recovered revision is
synthetic fixture state, not a live Linear result.

| State | Wide (120×40) | Compact (80×24) |
| --- | --- | --- |
| Local recovery snapshot | [Inspect](recovery-tui-wide-inspect.png) | [Inspect](recovery-tui-compact-inspect.png) |
| Clarified brief and decision reason | [Fields](recovery-tui-wide-fields.png) | [Fields](recovery-tui-compact-fields.png) |
| Multiline field editor | [Editing](recovery-tui-wide-editing.png) | [Editing](recovery-tui-compact-editing.png) |
| Scrolled source inspection | [Source](recovery-tui-wide-source.png) | [Source](recovery-tui-compact-source.png) |
| Frozen decision review | [Review](recovery-tui-wide-review.png) | [Review](recovery-tui-compact-review.png) |
| Scrolled comparison and confirmation | [Review end](recovery-tui-wide-review-end.png) | [Review end](recovery-tui-compact-review-end.png) |
| Unconfirmed reply with submission locked | [Uncertain](recovery-tui-wide-uncertain.png) | [Uncertain](recovery-tui-compact-uncertain.png) |
| Reloaded accepted clarification | [Recovered](recovery-tui-wide-recovered.png) | [Recovered](recovery-tui-compact-recovered.png) |
| Retained progress update decision | [Progress](recovery-tui-wide-progress.png) | [Progress](recovery-tui-compact-progress.png) |
| Shared delivery evidence review | [Evidence](recovery-tui-wide-evidence.png) | [Evidence](recovery-tui-compact-evidence.png) |
| Verification retry review | [Retry](recovery-tui-wide-retry.png) | [Retry](recovery-tui-compact-retry.png) |

This documentation pass inspected `internal/tui/goalrecovery.go`, relevant
recovery tests and the capture fixture, plus the `goals.go` and `app.go`
integration. It opened wide fields, compact review-end and compact uncertain
captures, comparing them with [incumbent wide fields](rollout-tui-wide-fields.png).
The sampled views retain the compact terminal utility grammar: dark ground,
monospace text, cyan breadcrumb, `settingsForm` field navigation, scrolling prose
and persistent contextual footer keys. The compact comparison wraps and scrolls
while confirmation remains at the bottom. No material visual drift was found in
this scoped documentation inspection; existing TUI components remain the visual
authority.

Recovery opens a local snapshot and isolates its decisions from parent polling
and competing goal controls. Available actions move from editable fields into an
independently frozen review, then require `a` to confirm. Source prose and the
before/after brief remain scrollable; the mutation retains the reviewed revision
and exact source or progress-update ID where applicable. Refreshing the Linear
brief is identified as an external read, while reloading the local snapshot does
not contact Linear. Clarification does not expand tasks, permissions or budgets;
progress dismissal retains history without claiming delivery; an evidence URL
does not establish verification. An unconfirmed reply preserves the review,
locks repeat submission and offers `f` to reload local state. A confirmed action
also reloads before another decision. These behavior claims are supported by
source and tests; screenshots alone do not establish live execution.

The fresh finish reviewer reports inspecting all twenty-two recovery captures,
[rollout wide fields](rollout-tui-wide-fields.png),
[rollout compact review](rollout-tui-compact-review.png), and relevant source/tests.
Its disposition is **ship**, with no material findings, and it independently ran
`TestGoalRecovery*` successfully. The implementation handoff reports focused
recovery tests and captures passing (`/tmp/lola-tui-recovery-focused.log`) and
full `make check` completing with exit 0
(`/tmp/lola-tui-recovery-make-check.log`); this documentation pass inspected both
log tails and did not rerun checks. The terminal-bounds regression covers
120×40, 80×24 and 40×12, including oversized errors, visible reload/locked keys,
and reaching the review reason and confirmation. No web/CSS detector was run:
this target is a Go terminal interface.

This is offline interface evidence only. No live daemon, Linear write, paid agent
call or live pilot was exercised, and this documentation pass did not
independently exercise keyboard navigation. The captures are review artifacts,
not product imagery. This ordinary Operate extension introduces no new visual
world or system rules. PRODUCT.md, DESIGN.md and `.impeccable/design.json` remain
absent; all earlier review entries and system documents are preserved.


## Improvement proposal triage in workspace learning

These eighteen offline WebKit captures render the actual Svelte components at
1100px and 500px widths, using sample proposal and decision data without a daemon
or external writes.

| State | Desktop (1100px) | Narrow (500px) |
| --- | --- | --- |
| Scoped proposal inspection | [Inspect](improvements-desktop-inspect.jpg) | [Inspect](improvements-narrow-inspect.jpg) |
| Exact source lesson version | [Source](improvements-desktop-source.jpg) | [Source](improvements-narrow-source.jpg) |
| Decision evidence editing | [Edit](improvements-desktop-edit.jpg) | [Edit](improvements-narrow-edit.jpg) |
| Frozen decision and confirmation | [Review](improvements-desktop-review.jpg) | [Review](improvements-narrow-review.jpg) |
| Unsaved reviewed decision exit | [Discard](improvements-desktop-discard.jpg) | [Discard](improvements-narrow-discard.jpg) |
| Unconfirmed write with repeat submission locked | [Uncertain](improvements-desktop-uncertain.jpg) | [Uncertain](improvements-narrow-uncertain.jpg) |
| Saved decision readback and retained history | [History](improvements-desktop-history.jpg) | [History](improvements-narrow-history.jpg) |
| Read-only export preview | [Export](improvements-desktop-export.jpg) | [Export](improvements-narrow-export.jpg) |
| Withdrawn proposal with retained decisions | [Withdrawn](improvements-desktop-withdrawn.jpg) | [Withdrawn](improvements-narrow-withdrawn.jpg) |

This documentation pass inspected `KnowledgeImprovements.svelte`,
`ImprovementBrief.svelte`, the `ProjectKnowledge.svelte` integration and proposal
component tests, plus `app.css`. It opened desktop history, narrow discard and
narrow uncertain captures and compared them with the incumbent
[narrow learning review](knowledge-narrow-review.jpg). The views retain the
existing dark canvas, muted metadata, restrained cyan actions, compact system
sans type, bordered fields and wrapping control rows. Long revisions and source
prose wrap at narrow widths. `app.css` and incumbent components remain the visual
authority; this ordinary Operate extension establishes no new world or system
rules. No material visual drift was found in this scoped inspection.

The catalog supports project or goal scope and paginated exact source references.
Source inspection retains recorded lesson versions and provenance, including
sources behind retained decisions for withdrawn proposals. Track, dismiss and
reopen actions freeze the proposal revision, scope and evidence before explicit
confirmation. Both editable and reviewed unsaved decisions require a discard
choice on exit. An unconfirmed write preserves its frozen review and locks repeat
submission until successful proposal/history readback; successful writes also
read back before another decision. The selector updates to that current state.
Exports are read-only previews of the inspected revision. Triage neither installs
rules nor authorizes implementation; withdrawn proposals expose history without
triage or export. These behavior claims rely on source and tests, not captures
alone.

The fresh finish reviewer inspected the original sixteen captures and identified
a stale proposal selector state and missing discard protection for a reviewed
unsaved decision. Both were fixed. The verdict pass inspected six recaptured
history/export/discard views, marked both findings resolved, and recommends
**ship at that two-fix scope**. This is not a claim that the verdict pass repeated
the entire initial review. The single detector result is empty
(`/tmp/lola-improvements-ui-detect.json`).

This pass inspected validation log tails: 70 frontend test files and 825 tests
pass (`/tmp/lola-improvements-ui-alltests-final.log`), Svelte checking reports zero
errors and warnings (`/tmp/lola-improvements-ui-types-reviewed.log`), and the
production build passes with dynamic-import and chunk-size advisories
(`/tmp/lola-improvements-ui-build-reviewed.log`). The implementation handoff
confirms final `make check` completed with exit 0 after the fixes and frontend
build (`/tmp/lola-improvements-ui-make-check-final.log`); its final package results
were inspected here. The documentation pass did not rerun checks.

This is offline interface evidence only. No live daemon integration, external
write, paid agent call or live pilot was exercised, and this pass did not
independently exercise keyboard navigation. Captures are review artifacts, not
product imagery. PRODUCT.md, DESIGN.md and `.impeccable/design.json` remain
absent, and all earlier review entries are preserved. No defect or incidental
surface composition is promoted into a design-system rule.


## Improvement proposal triage in the TUI

These twenty-four offline captures render actual Go TUI ANSI output through
xterm and WebKit at 120×40 and 80×24 terminal sizes, using fixture data without
a live daemon.

| State | Wide (120×40) | Compact (80×24) |
| --- | --- | --- |
| Scoped proposal catalog | [Wide](improvements-tui-wide-catalog.png) | [Compact](improvements-tui-compact-catalog.png) |
| Proposal inspection | [Wide](improvements-tui-wide-inspect.png) | [Compact](improvements-tui-compact-inspect.png) |
| Decision evidence editing | [Wide](improvements-tui-wide-edit.png) | [Compact](improvements-tui-compact-edit.png) |
| Frozen decision review | [Wide](improvements-tui-wide-review.png) | [Compact](improvements-tui-compact-review.png) |
| Review end and confirmation | [Wide](improvements-tui-wide-review-end.png) | [Compact](improvements-tui-compact-review-end.png) |
| Unsaved decision exit | [Wide](improvements-tui-wide-discard.png) | [Compact](improvements-tui-compact-discard.png) |
| Unconfirmed write and repeat lock | [Wide](improvements-tui-wide-uncertain.png) | [Compact](improvements-tui-compact-uncertain.png) |
| Retained decision history | [Wide](improvements-tui-wide-history.png) | [Compact](improvements-tui-compact-history.png) |
| Exact source lesson version | [Wide](improvements-tui-wide-source.png) | [Compact](improvements-tui-compact-source.png) |
| Read-only export preview | [Wide](improvements-tui-wide-export.png) | [Compact](improvements-tui-compact-export.png) |
| Withdrawn proposal history | [Wide](improvements-tui-wide-withdrawn.png) | [Compact](improvements-tui-compact-withdrawn.png) |
| Project or exact goal scope | [Wide](improvements-tui-wide-scope.png) | [Compact](improvements-tui-compact-scope.png) |

This documentation pass inspected `internal/tui/improvements.go`, its tests and
the `knowledge.go`/`app.go` integration. It opened wide catalog, compact review-end
and compact uncertain captures, comparing them with the incumbent
[wide lesson](knowledge-tui-wide-lesson.png) and
[compact recovery review](recovery-tui-compact-review.png). The extension retains
the dark terminal canvas, cyan breadcrumb, restrained neutral prose, monospace
text, existing `settingsForm` editing and persistent keyboard footer. Wrapped
content scrolls within the available body region while recovery and confirmation
keys remain visible. No material visual drift was found in this scoped inspection.

`i` from Workspace learning opens a paginated catalog scoped to the project or an
exact goal. Proposal and decision inspection retain exact revisions and historical
source lesson versions. Track, dismiss and reopen actions move through editable
evidence into a frozen review with a separate `a` confirmation; leaving an unsaved
decision requires a discard choice. An unconfirmed write retains that review and
locks repeat submission until successful proposal/history readback. Confirmed
writes also read back before another decision. Export is a read-only preview;
withdrawn proposals retain decision and source inspection without triage or export.
Triage records a decision and does not authorize implementation or install rules.
These behavior claims rely on source and tests, not screenshots alone.

The fresh finish reviewer reports opening all twenty-four captures, inspecting
source, tests and validation logs, and returning **ship** with no material
findings for this TUI extension. This documentation pass inspected the focused
test log (`/tmp/lola-tui-improvements-tests-final.log`) and full check log
(`/tmp/lola-tui-improvements-make-check-final.log`); their final package results
pass. The implementation handoff confirms full `make check` completed with exit 0.
The terminal-bounds regression additionally covers 40×12, oversized errors with
visible recovery keys, and long history scrolling. This pass did not rerun checks.
No web/CSS detector was run because the target is a Go terminal interface.

This is offline interface evidence only. No live daemon integration, external
write, paid agent call or live pilot was exercised, and this documentation pass
did not independently exercise keyboard navigation. The captures are review
artifacts, not product imagery. This ordinary Operate extension introduces no new
visual world or system rules. PRODUCT.md, DESIGN.md and `.impeccable/design.json`
remain absent; earlier review entries are preserved. No defect or incidental
surface composition is canonized into the design system.
