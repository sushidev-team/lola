# Self-improving workspaces and autonomous goals in Lola

Status: implementation resumed at the user’s request on
`feat/self-improving-workspaces` (2026-10-21). The full plan is not complete.
The earlier 75% estimate is superseded by the
[requirement-by-requirement audit](docs/workspace-goals-audit.md). Core phases
0–4 have automated evidence. Outcome recording/reporting is implemented; the
live pilot and representative comparisons remain incomplete. Improvement controls are implemented and reviewed in desktop and TUI.

Stack rollout addition: new multi-repository publication authorizations require
explicit compatibility requirements and a human merge order in `policy.rollout`.
The policy digest binds both to workers, bounded repair planning, specialist
reviews, verification and final linked PR descriptions. Goal store version 3
preserves the contract and legacy data through atomic writes and restart; old
authorizations keep their original digest and are labeled as lacking a reviewed
rollout. Publication recovery preserves already reserved descriptions across
template upgrades while rechecking all execution inputs. A versioned start
command prevents older daemons silently ignoring the extended authorization.
CLI, desktop and TUI accept the contract with the reviewed goal revision.
Both clients retain drafts when publication is disabled, freeze the contract
for authorization, hold uncertain requests without resubmitting, and show the
accepted contract in goal details. Legacy goals explicitly lack a recorded
merge recommendation. Multiline paste and long-detail navigation also work
in the terminal authorization flow. Final `make check`
passed after the versioned command change, alongside migration race checks,
focused worker/reviewer/repair/CLI/publication tests and frontend type checking
(zero errors or warnings). Tests cover preserved usage and legacy data, atomic
upgrade failures, exact policy identity and restart without repeated PR writes.

Desktop/TUI rollout verification is complete: the final frontend suite passes
67 files / 804 tests, type checking reports zero errors or warnings, and the
production build passes. Final `make check` passes with the refreshed desktop
bundle. Tests cover invalid/duplicate/foreign member order, UTF-8 requirement
bounds, retained unpublished drafts, frozen revisions, uncertain submissions,
overview isolation and multiline terminal paste. The independent UI reviewer
requested one error-visibility fix; its final verdict scored that fix resolved
(`ship`). Twenty-eight offline captures retain desktop/narrow and wide/compact
terminal evidence, including focused errors after bottom-of-form submission.

Recovery controls are implemented in desktop, TUI and CLI. A dedicated on-demand `goalRecovery` snapshot
now exposes the current brief, pending source, latest brief decision and progress
update, shared evidence URL and locally eligible recovery actions. It omits private
verification receipt paths and execution policy. Read/probe tests prove the source
goal and paid-call reservations are unchanged; the daemon endpoint makes no Linear
calls. Desktop bridge methods preserve reviewed revisions and source/update IDs
without automatic retry. Focused domain/daemon/bridge tests, the recovery domain
race check, frontend type checking and production build pass. Full `make check`
also passes (`/tmp/lola-recovery-make-check.log`). The desktop forms now expose
all six actions with separate review/confirmation, exact source/update identity,
required reconciliation reasons, uncertain-response locks and local readback.
They show accepted brief comparisons, progress resolutions and shared evidence.
Ordinary controls and selection are held while recovery is open. The TUI now
exposes the same six actions through **e** in goal details, with source inspection,
multiline editing, frozen review, a distinct confirmation key and persistent
recovery controls. Exact identities and paid retry semantics are unchanged.

TUI recovery validation passed `TestGoalRecovery*`, offline capture tests and
full `make check` (`/tmp/lola-tui-recovery-make-check.log`). Coverage includes
all six exact command payloads, frozen nested data, stale/mismatched replies,
unknown writes and failed local reloads, UTF-8 limits, multiline paste, draft
discard and persistent recovery keys down to 40×12. The independent reviewer
inspected twenty-two wide/compact captures and source, independently reran the
focused tests, and returned `ship` with no material findings. Recovery parity is
implemented; these fixture checks do not establish live interoperability.

Desktop recovery validation passed the full frontend suite (69 files / 814 tests),
zero-error/warning type checking, production build and final `make check`
(`/tmp/lola-recovery-ui-make-check-final.log`). Tests cover frozen revisions and
source/update identities, required reasons, uncertain replies and failed local
reloads, evidence URL validation, both paid retry actions and overview isolation.
The independent UI reviewer returned `ship` after inspecting all sixteen offline
desktop/narrow captures and the component source. This verifies the desktop
extension; TUI evidence is recorded separately, and live pilot interoperability
remains unverified.

Earlier verification: `make check` passed after the human acceptance desktop/TUI
controls and domain overview changes. Focused acceptance behavior tests cover
required evidence, frozen revisions, uncertain-response recovery and compact
terminal scrolling. The independent UI reviewer returned `ship` with no material
fixes across 24 desktop/narrow and wide/compact terminal captures. Earlier
acceptance/CLI tests and domain/policy race checks also passed.
Previously, `make check` passed again after the audit's launch-recovery
regression (both post-launch persistence failures also passed in a focused
uncached run). Earlier, `make check` passed (build, vet and full Go suite) after
proposal-editing storage, CLI, desktop bridge and desktop/TUI graph-editor changes.
The full TUI suite passed uncached with graph capture fixtures enabled. Ten new
behavior/viewport tests cover frozen edits, correlated responses, uncertain
recovery, graph remapping, scope/coverage, paste, discard and persistent controls.
Focused edit/migration
race checks also passed, including usage retention across later writes/recovery;
generated frontend bindings type-check without errors or warnings. Earlier checks
covered desktop and TUI knowledge curation, scoped mutations and durable capture
identities. Final TUI tests passed uncached after label-spacing corrections; the
final full build, vet and Go suite also passed.
The earlier frontend suite passed (67 files, 801 tests), including 18 focused
graph-editor/planner tests covering dependency remapping, reviewed identities,
uncertain recovery and acceptance of the saved revision. Frontend type checks
(0 errors/warnings) and production build passed. Earlier expiry preservation and
navigation guards passed all nine focused learning-panel tests. The earlier TUI goal planning,
publication and execution-authorization checks and offline captures also passed.
Earlier promotion, curation and contract race checks passed. Temporary Git fixtures
cover unmerged code, observed goal delivery, proof withdrawal, code drift, scope
and migration. Desktop/narrow fixture screenshots and scoped independent UI
reviews are retained under `.impeccable/review/`. The desktop learning review
returned `ship` with no material fixes, as did the independent TUI learning
review across eighteen wide/compact captures. The desktop task-graph editor also
received `ship` with no material fixes after ten desktop/narrow captures of
editing, repository scope, revision review, uncertain recovery and saved history.
The TUI graph editor received `ship` with no material fixes across eighteen
wide/compact captures of its task list, forms, review, recovery and history.
No live end-to-end pilot has run.

Improvement inspection foundations are implemented: a bounded catalog keeps
withdrawn proposals with decisions discoverable, and paginated source references
retain exact lesson versions beyond the 20-source preview. Decision history uses
separate source pages, while the existing CLI full receipts remain unchanged.
Typed desktop reads, triage and export methods preserve project/goal scope and
reviewed revision without automatic retries. Source/history reads cannot authorize
an obsolete proposal. Focused store/daemon/bridge tests, the store race check,
frontend type checking and production build, and full `make check` pass
(`/tmp/lola-improvementviews-make-check.log`). Desktop and TUI improvement forms are now implemented.

Desktop Workspace learning now opens an improvement catalog with explicit
project/goal scope, paginated exact source versions, retained decision history
and withdrawn-proposal inspection. Track/dismiss/reopen decisions freeze the
reviewed proposal and require separate confirmation. Unconfirmed responses lock
the decision until local readback; failed readback keeps that lock. Export is a
revision-bound read-only preview. The frontend suite passes 70 files / 825 tests,
type checking reports zero errors/warnings, and the production build passes.
The independent reviewer requested consistent selector state after readback and
discard protection for reviewed unsaved decisions. Both were fixed; its verdict
pass scored both resolved (`ship`). Eighteen offline desktop/narrow captures
retain the evidence. Final `make check` passes against the refreshed frontend bundle
(`/tmp/lola-improvements-ui-make-check-final.log`).

TUI improvement controls now open with **i** from Workspace learning. They
support exact project/goal scope, paginated catalog/source reads, historical
lesson versions and decisions, withdrawn-proposal inspection, triage and export
preview. Reviewed decisions freeze scope, source revision and evidence. Lost
responses require readback; failed reads retain the lock. Source summaries use
the incumbent learning view with full provenance available separately. Tests
cover the three triage payloads, pagination, stale/foreign replies, historical
versions, export failures, multiline paste, draft protection, long evidence and
recovery keys down to 40×12. Focused tests and final `make check` pass
(`/tmp/lola-tui-improvements-tests-final.log`,
`/tmp/lola-tui-improvements-make-check-final.log`). Twenty-four wide/compact
fixture captures were inspected by the independent reviewer, who returned
`ship` with no material findings. No live pilot ran.

### Checkpoint and quick user testing

This checkpoint is intended for a quick UI/CLI look and development testing.
The source builds and the automated suites run against fakes and temporary
repositories. No live autonomous pilot, deployment, main-branch merge, or installed
daemon replacement has been performed. UI screenshots use clearly labeled fixtures.

Available for inspection: project/stack Execution settings; goal overview,
tasks, limits, delivery and steering controls in desktop/TUI; CLI goal planning,
authorization and recovery; knowledge capture/curation/export and improvement
proposals; specialist reviews
and finding decisions. Specialist configuration, goal planning, local plan
acceptance and reviewed Linear publication are available in desktop and TUI.
Explicit execution authorization is available in desktop, TUI and CLI.
Desktop and TUI planners also support reviewed task-graph edits and revision history.
Desktop and TUI Workspace learning now support candidate/correction capture, provenance,
version-bound acceptance/dispute, watched code, expiry and read-only export preview.

Remaining before full-plan completion is enumerated in the
[audit finish checklist](docs/workspace-goals-audit.md#finish-checklist):
representative outcome comparisons and a bounded live pilot. Generic
worker discoveries can already be curated with explicit evidence; automatic
verification of arbitrary discoveries is not an additional completion condition.
Immutable active policy is an intentional authorization boundary, not unfinished
coordinator permission to increase scope or budgets.

The audit's launch-recovery regression is complete: actual runtime launch
receipts with fake tmux survive injected post-launch session and goal snapshot
failures. Reopened stores recover the same attempt and run with one launch,
preserved worker output and no premature dependency release.

Optional human delivery acceptance is now implemented in the domain, daemon,
CLI, desktop and TUI. New execution policies can require a separate evidence-backed decision after
observed complete final PR delivery. It retains the reviewed revision, policy,
final proof and commit manifest; exact retries preserve the original decision.
Child task delivery remains separate. The goal store upgrades atomically to
version 2 only when this option is used, and older writers fail closed. Both
clients review the authorization choice, show the pending decision and saved
evidence, and require nonempty evidence before submitting the reviewed revision.
Uncertain replies hold further submission until a read-only refresh. The TUI
review scrolls long criteria and commits while preserving typed/pasted evidence.

Quick-look setup and exact current commands are in
[the implementation guide](docs/workspace-goals.md#quick-look-at-this-checkpoint).
Resume from this list; green checks at this checkpoint do not complete the plan.

Learning maintenance addition: acceptance can attach committed file/directory
references with `learn accept --watch member:path`. Every guidance consumer checks
watched code before use; changed code returns lessons to candidates, unreadable
code is omitted for that request, and immutable delivery history is retained.
The existing five-minute maintenance pass and `learn maintain --project ...`
also handle expiry. Git reads share a bounded deadline and perform no fetch or
working-tree edits. Unwatched historical lessons remain explicitly untracked.
This implements freshness revalidation, not automatic correctness verification.

Improvement proposals addition: recurring scoped reports and explicit prevention
ideas now yield inspectable test/lint/skill task briefs; mandatory-policy requests
remain separate proposals. CLI triage is revision-bound and preserves history;
export writes reviewable artifacts only. Repeated reports within one session do
not increase the occurrence count, and operator captures are counted once.
Neither recurrence nor triage activates a lesson, starts work, or installs a rule.
Goal workers now receive the same bounded, recorded knowledge briefing as ordinary
workers; launch recovery preserves the original supplied snapshot.

Worker dispute addition: specialty feedback now supplies finding IDs/revisions
through independent guarded handoffs or goal repair prompts. Bounded worker report
files can dispute only daemon-owned session/task findings, with recorded task
ancestry for repairs and no authority to dismiss or verify. Blocking disputes hold
delivery and goal progress for human resolution; one worker dispute per finding
prevents automatic debate loops. Independent verdicts and evidence remain intact.

Reviewer provenance addition: each new review preserves its requested model,
agent, exact instruction and SHA-256, and host/container execution environment.
Those facts participate in review identity and calibration grouping; model/prompt
changes invalidate cached gates and pending feedback. Adapters refuse a changed
invocation before launching. Legacy reviews retain their original history as
unknown provenance and require a fresh review for current gates. The specialist
store reads version 1 and writes version 2, preventing old binaries from dropping
new provenance. Rollback behavior and model-resolution limits are documented in
the implementation guide. This supplies comparison evidence, not measured
improvement or proof of which model a provider actually served.

Verified learning promotion addition: verified findings can now derive scoped
project guidance after independent fix-review evidence matches committed code on
the configured default branch. Goal findings additionally require observed full
delivery. Promotion preserves the source candidate, exact finding/review links
and automatic code references. Retrieval rechecks both proof and code; disputes,
invalidated verification and code drift retract guidance, while missing evidence
omits it. Replays cannot undo curation. Explicit human acceptance can replace
the automatic proof while retaining its history. Worker assertions, conflicts
and mandatory rules remain outside this automatic path. Knowledge format version
2 protects the new provenance from older writers; the guide documents rollback.

TUI specialist settings addition: Settings → Review now creates and removes
profiles and edits their provider, triggers, rubric, project/path scope, evidence
and limits. New profiles start disabled; changes remain a draft until the whole
configuration validates and saves. Multiline rubrics survive unrelated settings
edits. Compact terminals retain the active tab, focused field and wrapped errors.
Offline normal/compact fixture captures exercise the actual modal renderer; they
do not establish live specialist execution.

TUI goal authoring addition: Goals now offers a brief form, saved proposals,
scrollable task/coverage/scope/source review and acknowledgment before accepting
the exact proposal revision. Requests retain their identity before dispatch;
uncertain replies require read-only recovery, never another automatic paid call.
Local goals can be published through a separate revision-bound review. These
actions do not authorize execution.

TUI execution authorization addition: a published goal can load read-only
settings, edit repository paths, checks/evidence, specialist selections and finite
limits, then inspect a frozen policy before explicitly authorizing it. Required
specialists cannot be removed. The exact goal revision and policy are submitted;
uncertain responses disable repetition and require a fresh overview. Draft edits
have discard protection. Offline captures and tests cover the terminal workflow;
no real goal was authorized or started during this increment.

Implemented foundations: durable goal reservations and recoverable Linear
publication; ordinary-dispatch exclusion; scoped knowledge capture, curation,
retrieval and export; independently keyed specialist profiles and review gates.
The isolated Git workspace, disposable acceptance-check runner, and recoverable
stack-integration engines are implemented and connected to the goal controller.
Independent checks and artifact-backed specialist reviews gate each integration;
dependent tasks receive the verified repository combination. A separate final
pass checks the whole goal before marking its local artifact ready. Concrete
failures can schedule bounded repair attempts; uncertain results hold, with an
explicit retry path that preserves evidence and refuses to abandon partial
ref updates. Project/stack availability, accepted policies, private workers,
deadlines and steering are durable. Verified goals can now publish only their
reserved goal branches and draft PRs through a recoverable journal; observed
human merges complete delivery across all repositories. Screenshot sharing is
explicit, and final evidence is rechecked before upstream writes. Repository-informed
coordinator proposals now preserve exact source and lesson evidence, validate
coverage and routing, and require review before retry-safe local acceptance.
Planning runs are isolated, bounded, and counted in the accepted goal's call budget.
Task ownership is recorded per attempt, restricts parallel dispatch to disjoint
write scopes, and gates submitted diffs; shared context members can be read-only.
Opt-in autonomous replanning can decompose conclusively rejected tasks within
the accepted ownership and budgets, preserving original criteria and task history.
Replacement publication recovers stable Linear identities before dispatch resumes.
Target-refresh primitives now fetch exact upstream commits into independent
daemon stores, preserve both histories in candidates, recover partial stack
updates through the existing manifest journal, and bind final evidence and
publication to the captured target combination. Conflicts and rewritten target
history retain the goal unchanged. Accepted finite refresh limits now drive
durable controller reservations, pause/resume and explicit verification retry.
Previous final and delivery evidence remains in history; a new whole-goal final
pass gates publication after refresh. Interrupted paid reviews hold rather than
rerun silently, and refresh never extends the original execution budgets.
Refreshed publication preserves existing PR identities and human descriptions,
updates only verified descendant heads, and posts separately marked final evidence
with lost-response reconciliation. Closed, deleted, or foreign remote state holds.
When upstream moves again before integration, the controller archives the previous
target capture and reserves another within the same accepted refresh limit.
It confirms old check containers have stopped, retains check/review evidence,
and requires the new target to contain every earlier capture. Any ref-update
intent prevents supersession and must recover with its original proof.
Explicit existing-issue adoption now preserves original text, labels, parent,
and release milestone. A reserved related-issue link exposes membership and
accepted criteria in the goal brief; drift holds execution without rewriting
human work. Local claim serialization and relation-aware dispatch protect adopted
issues from ordinary workers, including in a different Lola home after publication.
Goal specialists now run in bounded containers with read-only candidate and
captured-evidence mounts, explicit model credentials, and exact local image IDs.
Durable launch and cleanup receipts gate integration and publication; interrupted
reviews require explicit retry and cannot silently fall back to host execution.
Private worker usage now reaches the durable goal counters and project/global
ledger for Claude, Codex and OpenCode. Repeated scans preserve observed high-water
totals, expose missing/partial telemetry, and stop active goal work on an observed
limit without removing files. Coordinator and specialist CLI event streams now
contribute durable numeric receipts, including failed and unaccepted planning
calls. Acceptance reuses the same ledger identity to avoid double charging.
Headless consumption is observed when the call returns, so missing events and
in-flight consumption remain explicitly unknown rather than an exact billing cap.
The desktop project hub and TUI now expose cached goal briefs, task attempts,
usage coverage, integrated revisions, delivery links and revision-bound execution
controls. Human action review preserves its selected goal and exact revision;
stale responses and failed commands cannot silently retry against newer work.
Validation accounting now attributes owned check receipts, observed wall-clock
time and configured resource ceilings to every retained verification operation.
Missing/interrupted consumption and unmeasured CPU/memory/dollar costs remain
explicitly unknown. Parent brief edits now produce bounded, durable source
proposals; explicit revision acceptance rechecks Linear and retains the existing
task plan and policy. It archives superseded final evidence, holds work paused,
and requires another final pass within the original budget. Uncertain publication
and in-flight integration cannot be discarded by accepting new prose.
Goal progress now reaches the existing Linear parent through a coalesced durable
comment outbox, with stable UUID recovery, explicit unknown-send resolution,
and preservation of human edits/deletions. Narration never changes verification
or delivery status and never includes raw tool output.
Child workflow projection now reuses configured team states: running work and
verified goal integration require a started/review state; completion waits for
observed target delivery. Durable state-transition intents recover lost responses
and preserve observed human changes. Premature Linear Done cannot authorize work.
Project and stack settings now expose goal-mode availability and workspace
isolation in the desktop and TUI. Older clients preserve these settings; stack
members cannot override owner settings. Enabling availability does not authorize
an individual goal, and invalid isolation or Linear placement cannot be saved.
Specialist findings now have durable reported/accepted/disputed/dismissed/fixed/
verified histories, revision-bound idempotent operator decisions and a calibration
count grouped by profile, provider, rubric and repository. A verified fix requires
a clean independent review of its exact commit and contract. Decisions never
rewrite review verdicts or open integration/merge gates; retries retain earlier
findings and verification receipts.
Desktop Review settings now expose Backend, UI/UX, Security, Integration and
Performance specialist presets, independent provider selection, ordinary PR
triggers, required gates, rubric/scope/evidence and finite limits. New profiles
start disabled, older clients preserve profiles, and invalid provider removal
cannot partially save. Go and frontend suites, type checks and builds passed;
desktop/narrow fixture inspection and independent UI review passed (`ship`).
Desktop goal planning now accepts outcome/criteria/non-goals and explicit bounded
coordinator requests, lists durable saved proposals, exposes task and source
reviews, and accepts a local goal at the reviewed revision. Unknown responses use
read-only recovery and never repeat a paid call automatically. Publication now
has a desktop review action with a lock-protected revision check before any Linear
effect. Execution authorization loads configured repositories, preserves required
specialists, requires explicit acceptance checks and evidence, captures finite
limits and optional draft-PR/replanning permissions, and reviews the exact policy
before starting. It exposes the retained goal/task contract and rejects uncertain
resubmissions until the user reloads current state. No live writes or workers were
started during fixture verification.
Desktop knowledge curation now uses retained capture identities, exact
lesson/version/scope decisions, explicit conflict replacement, committed code
references and preserved expiry. Full provenance and export contents are
inspectable; exports write no repository files. Uncertain mutations require a
refresh before another decision. Worker claims still cannot activate lessons.
TUI Workspace learning now provides the same capture/curation/export commands
through a project-detail entry, keyboard forms and a separate reviewed decision.
It preserves exact versions, capture identities, conflict choices, code references
and expiry, protects drafts/paste, ignores old-project replies and blocks uncertain
resubmissions. Offline wide and compact terminal fixtures cover inspection,
editing, validation, review and recovery.
Proposal editing addition: `goal edit-plan` now replaces a reviewed task graph
without another coordinator call or Linear writes. Exact project/revision/edit
identities protect concurrent decisions and retries. Original model output,
source evidence and usage remain preserved beside bounded operator edit history.
Current routing is checked, stale acceptance is rejected, and accepted goals or
execution repair proposals cannot be edited through this path. Proposal storage
reads formats 1/2 and upgrades atomically only on the first edit; older binaries
reject format 2 instead of dropping history. The desktop planner now provides task-graph editing forms: title/description,
acceptance criteria, coverage, dependencies, repositories, paths, labels and open
questions, plus add/remove/reorder. Dependency references survive moves; moves
across a prerequisite and deletion of referenced tasks are blocked. A frozen
revision/edit identity and reason are reviewed before save. Pending and uncertain
saves block resubmission; recovery reads the saved proposal. The accepted revision
must be reviewed again. History and original model output are inspectable.
The TUI now provides the same authoring path through a task list, per-task forms
and notes/reason form. It validates the full graph before freezing a review,
preserves dependency references, confirms removal/discard, and keeps pending or
uncertain saves from being repeated. The exact edit request and saved original
model output are inspectable. Multiline paste reaches planner forms; review and
pending states reject it. Help and error text are bounded so compact terminals
retain the current field and action controls.
The full plan is not complete. Comparison recording/reporting is implemented;
a bounded live pilot and representative comparisons remain.
The audit above defines the accepted reconciliation boundaries. See
[current commands](docs/workspace-goals.md).
Research and repository inspection: 2026-10-21.

This plan combines persistent workspace learning, specialist critique, and bounded
autonomous execution. It incorporates the decision to keep autonomous work visible
inside existing Linear projects, retain label-based repository routing, and leave
Linear initiatives untouched.

## 1. Recommended direction

Build three connected capabilities:

1. **Workspace knowledge:** capture corrections and discoveries automatically;
   activate reusable lessons when their evidence and scope justify it.
2. **Specialist review profiles:** independent backend, UI/UX, security, and
   integration evaluations, selected according to the work.
3. **Autonomous goals:** a coordinator decomposes an authorized objective into
   tasks; Lola schedules workers, verifies results, and integrates accepted work
   into isolated goal branches. Humans merge the final PRs into main.

Make the work understandable in Linear before enabling autonomous execution.
Use an existing Linear project for each configured project or stack. For Nori,
all participating repositories remain in the existing Nori Linear project and
task labels continue to select the relevant stack members.

Do not create or modify initiatives. Do not introduce a new Linear project per
repository or per goal.

The default goal presentation is a parent issue containing the goal brief, a
milestone grouping its tasks where appropriate, and child issues representing
deliverables. Lola retains execution records that Linear cannot represent:
attempts, leases, verified commits, budgets, integration receipts, and recovery.

## 2. Research and its practical implications

These sources support the architectural patterns; they do not establish that a
particular reviewer or memory configuration will improve Lola without evaluation.

| Evidence | Implication |
| --- | --- |
| Repository context files did not generally improve success in a recent evaluation and increased inference cost by over 20% on average. | Keep always-loaded context small and measure lesson usefulness. [AGENTS.md evaluation](https://arxiv.org/abs/2602.11988) |
| ACE separates generation, reflection, and curation and uses incremental updates to evolving playbooks. | Store individual, attributable lessons; avoid repeatedly rewriting one giant summary. [ACE](https://arxiv.org/abs/2510.04618) |
| OpenAI describes a short AGENTS.md indexing a structured repository knowledge base. | Use LEARNINGS.md as an index into focused knowledge. [Harness engineering](https://openai.com/index/harness-engineering/) |
| Every explicitly adds a compound step after planning, work, and review. | Capture reusable solutions as an ordinary part of finishing work. [Compound engineering](https://every.to/guides/compound-engineering) |
| Anthropic's application-development harness separates generation and evaluation, including browser interaction against explicit criteria. | Give reviewers observable acceptance criteria and access to appropriate artifacts. [Application harness](https://www.anthropic.com/research/harness-design-long-running-apps) |
| Cursor reports coordination problems with equal-status agents sharing task ownership, then better results with planners, workers, and a judge. | Let Lola own scheduling and let workers focus on bounded tasks. [Autonomous coding experiments](https://cursor.com/blog/scaling-agents) |
| Controlled multi-agent experiments find substantial variation with task structure and coordination overhead. | Parallelize independent work and run specialists selectively. [Scaling agent systems](https://arxiv.org/abs/2512.08296) |

For long work, explicit progress artifacts, incremental implementation, and
verification at session boundaries are useful starting patterns.
[Long-running agent harnesses](https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents)

## 3. Existing Lola capabilities to reuse

| Existing implementation | Extension |
| --- | --- |
| [Task context](internal/runtime/context.go): notes survive respawns and forks through .lola/context. | Add goal, project, and stack knowledge above task notes. |
| [Planner](internal/planner/planner.go) and [plan application](internal/daemon/plan.go): validated decomposition into Linear sub-issues and dependencies. | Add repository-informed planning, a durable goal contract, and bounded replanning. |
| [Stack selection](internal/config/stack.go) and [stack runtime](internal/runtime/stack.go): labels select member worktrees for one session. | Preserve routing and track a goal revision across member repositories. |
| [Review providers](internal/config/reviewprovider.go), [review worker](internal/daemon/reviewworker.go), and [review gates](internal/daemon/reviewgate.go). | Add named profiles, evidence, and profile-specific verdicts. |
| [Merge queue](internal/daemon/mergequeue.go): serialized, checked merging into the configured default branch. | Reuse verification patterns in a separately governed goal integration queue. |
| [Usage ledger](internal/usage/ledger.go) and [budgets](internal/config/budget.go). | Attribute all goal work and bound calls and runtime, beyond holding new dispatches. |
| Checkpoints, adoption, dirty-safe cleanup, sandboxing, and gitguard. | Preserve recovery and isolation in the new workspace topology. |

Important existing boundaries:

- The planner currently requires a human apply step. Keep that command's behavior;
  add a separately authorized goal execution path.
- Existing review guards often key by provider kind, not by named specialty.
- The merge queue targets the default branch and must never accidentally adopt
  final goal PRs.
- Agent reports, claim audits, status overlays, and overlap hints are display-only.
  Do not repurpose them as authoritative scheduling or acceptance state.
- Current budget limits hold dispatch; they do not stop running sessions.
- The current Linear client has issue creation and dependency relations, but its
  IssueCreate model has no milestone field and its API has no milestone methods.

## 4. Linear representation: challenge and recommendation

### 4.1 Why a milestone fits the user's organization

The user confirmed that Nori's repositories share one Linear project and are
distinguished by labels. This matches Lola's stack model. Repository count does
not determine Linear project count.

A milestone offers a named grouping, description, optional date, and issue
filtering within that project. Milestones are project-local; they cannot be
shared across projects. Their displayed progress begins with started issues,
not only completed issues. [Linear milestone documentation](https://linear.app/docs/project-milestones)

An initiative groups projects and would introduce an unnecessary organizational
level for this workflow. The plan excludes initiative integration.
[Linear initiative documentation](https://linear.app/docs/initiatives)

### 4.2 Concrete wrinkle: Nori already uses milestones

Read-only inspection found these existing milestones in the
[Nori project](https://linear.app/sushi-dev/project/nori-77aa4cae0777):

- First Public Release
- Hypercare

A task has one milestone association. Giving an existing task a new goal
milestone would replace its release-phase grouping. Do not silently reorganize
the user's backlog to accommodate Lola.

Therefore support two presentation choices using the same underlying Goal:

| Situation | Representation |
| --- | --- |
| New bounded goal with newly generated tasks | New milestone in the existing project, plus a goal parent issue and child tasks |
| Existing work already organized under a release milestone | Keep that milestone; use the goal parent issue and child/related issues for goal membership |

The second choice is a lightweight fallback, not a second execution system.
For an existing issue that already has a parent, preserve that relationship and
link it as related work rather than silently reparenting it.

The proposal is **one goal may have a milestone**, not **a goal is a milestone**.
Stable goal identity must survive renaming, moving, or removing the Linear
presentation. A milestone must not be the sole record of execution ownership.

### 4.3 Proposed Linear shape

~~~text
Existing Linear project: Nori
  Goal milestone: Team invitations             [when a new milestone fits]
    Parent issue: Goal — Team invitations
      Outcome, acceptance criteria, non-goals, constraints
      Current verified summary and links to previews / final PRs
    Child issue: Implement invitation API       [existing API routing labels]
    Child issue: Implement invitation UI        [existing UI routing labels]
    Child issue: Verify invitation flow         [both routing label sets]
~~~

Label names above are illustrative. Resolve actual label UUIDs and member
selection from current configuration; do not invent labels or new repository
mapping conventions.

The parent contains the editable goal brief. The milestone description contains
a short summary and a link to that parent, avoiding two independent copies of
the acceptance contract.

Create issues for deliverables and material follow-up work. Retries, review
passes, and repair attempts normally remain attempts on the same task, not a
stream of new issues. Separate repair issues only when the work has become a
distinct deliverable within the authorized scope.

### 4.4 Three concepts must stay separate

1. **Membership:** which goal includes an issue.
2. **Routing:** which repositories its labels select.
3. **Eligibility:** whether dependencies, authorization, health, and budget allow
   it to run now.

Assigning a milestone or an application label does not authorize execution.
Adding an arbitrary issue to the milestone does not silently enroll it in an
active autonomous goal; Lola presents it as a scope-change candidate.

The current planner copies placement and labels from the parent. Goal planning
must instead compute each task's application-routing labels independently,
while preserving unrelated labels and respecting the configured polling rules.
Otherwise every child could inherit every repository selected for the parent.

Validate the selected members against the task's intended scope before launch.
Preserve the existing rule that members without routing labels are always
included; account for those members when validating permissions and scope.

### 4.5 Authority and synchronization

| Information | Authority |
| --- | --- |
| Human-authored outcome, acceptance criteria, priority, and discussion | Linear, with a revision accepted by Lola for execution |
| Budget, allowed actions, protected refs, and execution authorization | Explicit Lola goal policy |
| Task attempts, leases, dependency satisfaction, verified SHAs, integration | Lola's durable goal store |
| Code and PR/check facts | Git and GitHub observations |
| Progress summary shown in Linear | Lola-maintained projection of those facts |

Humans can edit the brief in Linear. Lola detects substantive changes, records a
new proposed revision, and stops affected new dispatches until it has reconciled
the plan. An edit or comment cannot enlarge permissions or weaken checks.
Comments are discussion unless handled through a future explicit, authenticated
command interface.

Use UUIDs for bindings and stable operation/task IDs for reconciliation. Write
only Lola-owned summary sections, preserving human prose. Coalesce meaningful
updates: a blocker, accepted plan change, verified integration, or final review
readiness. Avoid posting every tool call or heartbeat.

Changes in Linear are not an exact runtime control plane: moving an issue to
Done does not prove verification; canceling a prerequisite does not prove its
output exists. Reconcile those events as stop/replan signals and do not unlock
dependent work automatically.

### 4.6 Progress and completion semantics

Show explicit evidence counts, for example:

~~~text
Execution: 4 of 6 tasks integrated into the goal branch
Acceptance: 7 of 9 goal criteria verified
Delivery: final PR awaiting human merge
Blocker: invitation expiry behavior needs a product decision
~~~

These are example values, not current Nori status.

Keep child issues in an appropriate existing started/review state after goal
integration; do not automatically mark them shipped. Default completion occurs
after final target-branch delivery is observed. The parent can additionally
require human acceptance. Canceled or superseded tasks remain distinct from
verified tasks.

Map to configured team statuses rather than creating workflow states
automatically. Inspect existing GitHub-to-Linear status automations before the
pilot: a task PR merged into a goal branch must not silently mark the issue
delivered. Use local integration receipts where intermediate PR automation would
produce misleading completion.

### 4.7 Creation and recovery

Persist the plan and intended Linear bindings before any create call. Create
the parent, optional milestone, child issues, and dependency relations through
a recoverable operation journal. Record successful external IDs immediately.

Never rely on withholding trigger labels alone: projects can poll by state or
without labels. Goal-managed issues and the parent must be excluded from normal
dispatch, including before provisioning finishes. Persist reservations before
creation and include a recoverable goal/task marker in the initial issue payload.
Unknown or partially provisioned goal markers hold dispatch until reconciled.

Use provider-supported idempotency or stable client IDs if verified available;
otherwise reconcile ambiguous creation outcomes before retrying. A lost response
is not permission to create a second milestone or issue.

Only release a task after all its bindings, scope, and dependencies are complete.
On Linear failure, keep existing artifacts, show sync degradation, and hold new
dispatch and integration requiring current scope. Already-running work may
checkpoint safely. Deleting or archiving a binding does not authorize recreating
it or continuing invisibly.

## 5. Workspace learning

### 5.1 Memory layers

| Layer | Purpose | Storage |
| --- | --- | --- |
| Task notes | Decisions, dead ends, next actions | Existing .lola/context |
| Goal context | Shared contracts and provisional discoveries | Goal context in Lola's home |
| Project/stack lessons | Reusable corrections and operational knowledge | Versioned Lola knowledge store |
| Portable documentation | Human-readable, team-shared lessons | LEARNINGS.md index and focused docs/learnings files |
| Enforced practices | Mechanically checkable invariants | Tests, lint rules, scripts, CI |

Claude Code has its own repository-scoped auto memory shared across worktrees.
Lola still needs portable, attributable knowledge across its pluggable coding
agents. Supply a selected knowledge snapshot through the existing briefing path.
[Claude Code memory](https://code.claude.com/docs/en/memory)

### 5.2 Capture and promotion

Capture candidates from explicit corrections, accepted review findings,
reproduced failures and fixes, and non-obvious discoveries. Each entry records:

- Stable ID, concise lesson, applicability, and rationale.
- Project/stack, affected paths, and relevant environment.
- Source issue/session/review and evidence commits or artifacts.
- Candidate/active/disputed/superseded status and verification date.
- Relationships to duplicate, conflicting, or superseded lessons.

Automatically activate explicit user corrections within their stated scope.
Activate technical findings only with adequate verification; otherwise retain
them as candidates. A resolved thread or several agents repeating a claim is not
independent proof.

Changes to mandatory architecture policy, permissions, or acceptance rules are
reviewable proposals. Automatic capture does not grant authority to rewrite
AGENTS.md or publish changes to the default branch.

Keep experimental goal findings scoped to that goal until validated against the
accepted project code. Never promote one-off environment failures into universal
project instructions.

### 5.3 Retrieval and maintenance

Start with scope/path/tag filtering and text search. Inject a bounded relevant
bundle and allow explicit lookup. Record the lesson versions supplied to each
session. Add embeddings only if measured retrieval failures warrant them.

Use a single writer for canonical knowledge. Workers submit candidates rather
than concurrently appending to one shared Markdown file. Preserve provenance,
detect contradictions, expire environment-specific information, and revalidate
lessons when their referenced code changes.

Treat captured text as untrusted evidence. Exclude secrets, constrain its scope,
and never turn a learned suggestion into executable authority.

A recurring mistake should lead to a proposed regression test, lint rule, or
reusable skill when possible. Apply the same review and verification requirements
to those changes as ordinary implementation work.

## 6. Specialist review profiles

Start with general correctness plus conditionally selected profiles:

| Profile | Trigger | Required evidence |
| --- | --- | --- |
| Backend | API, persistence, daemon, concurrency | Contracts, invariants, failure paths, targeted checks |
| UI/UX | Screens and interaction changes | Running app, screenshots, keyboard flows, loading/error/empty states |
| Security | Credentials, authorization, execution boundaries | Trust boundaries and concrete attack paths |
| Integration | Cross-module or cross-repository work | Combined behavior and compatibility checks |
| Performance | Explicit performance work or relevant hot paths | Measurements and benchmarks |

Each profile defines a versioned rubric, provider/model selection, tools,
trigger conditions, limits, and acceptance significance. A title such as
"senior backend engineer" is not a sufficient specification.

Use fresh reviewer context with the task contract, relevant lessons, source,
diff, and verification artifacts. Do not initially prime the reviewer with a
lengthy worker defense of its implementation. Different models are optional;
diversity is not proof of correctness.

UI/UX evaluation needs a project-specific rubric and reference examples.
Preserve earlier checkpoints and cap polishing rounds: subjective scores can
rise while the user's preferred design gets worse.

Findings carry the violated requirement, location/reproduction, evidence,
severity, confidence, and suggested fix. Track reported, accepted, disputed,
fixed, and verified separately. Workers may dispute findings with evidence.
Unresolved blocking disputes go to a human rather than an endless debate.

Add profiles above the existing provider execution layer. Preserve one provider
per kind while allowing several profiles to use it. Key new guards and verdicts
by profile, repository, head/base inputs, and rubric version. Migrate legacy
records explicitly and keep old configuration working.

Reviews remain bounded background jobs. Source review is read-only; tests and
browser work use disposable validation environments. Corrections flow through
the existing sanitize, idle, ownership, and empty-composer gates.

## 7. Goal contract and coordinator

Enabling goal mode on a project/stack makes the feature available. Starting a
particular goal authorizes its bounded work; it does not authorize arbitrary
continuous maintenance.

The goal contract includes:

- Outcome and observable acceptance criteria.
- Non-goals and allowed repositories/areas.
- Existing Linear project, goal issue, optional milestone, routing configuration.
- Starting commits, goal branches, and final delivery targets.
- Required verification and reviewer profiles.
- Allowed external actions and integration policy.
- Concurrency, task count, runtime, spending, retry, and review-round limits.
- Conditions for pausing and the final deliverable.

The coordinator inspects relevant code and knowledge, proposes a dependency
graph, and responds to verified outcomes. Lola validates and executes the plan.
The coordinator does not hold broad write credentials or directly mutate
scheduler state.

Every task includes a deliverable, acceptance criteria, selected repositories,
dependencies, expected ownership boundaries, and verification requirements.
Establish shared contracts before parallel implementations diverge.

Begin with two workers and increase only for independent work. Permit bounded
repair and decomposition within the authorized scope. Expanding scope, changing
the outcome, weakening acceptance, or raising limits requires a new user
decision.

Persist Goal, Task, Attempt, Verification, and Integration as distinct records.
Track goal state separately from the existing agent and delivery axes:

~~~text
draft → planning → running → verifying → ready for review → accepted
                     ↘ paused / blocked / failed / canceled
~~~

"Ready for review" means the agreed goal artifact is verified and available for
human inspection. It does not mean merged or deployed.

Checkpoint before external effects, record outcomes, and reconcile ambiguous
results after restart. Recovery must not duplicate tasks, workers, PRs, or
integration. A versioned Go store with atomic snapshots and a durable action
journal fits Lola's architecture; a new orchestration framework is not required.
Persistence and cross-run knowledge are separate concerns.
[LangGraph persistence](https://docs.langchain.com/oss/python/langgraph/persistence)

## 8. Isolation and integration

### 8.1 Branch topology

~~~text
main at a recorded commit
  └── goal/<goal-id>
        ├── task/<task-id>-<attempt>
        ├── task/<task-id>-<attempt>
        └── task/<task-id>-<attempt>

verified task output → goal branch
goal branch → final PR → human merge
~~~

Use a separate goal clone/store so autonomous work cannot write the original
checkout's Git metadata. Linked worktrees share refs and configuration; a branch
name alone is not a security boundary.
[Git worktree documentation](https://git-scm.com/docs/git-worktree)

Workers must also lack write access to the authoritative integration store.
Use private worker repositories/worktrees or an equivalent enforced boundary;
mounting one shared writable Git store into every worker would undermine that
guarantee. Reuse Lola's runtime and sandbox seams with this explicit change in
repository topology.

Keep upstream write credentials outside workers. A narrow publisher validates
permitted branch and PR operations; it rejects default/release branch writes and
merges. GitHub rulesets add a server-side boundary with automation excluded from
relevant bypass rights. Token permissions alone should not be assumed to provide
branch-level restriction.
[GitHub rulesets](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/available-rules-for-rulesets)

Final goal PRs are excluded from Lola's default-branch merge queue even if that
queue is enabled and the PR is approved. This exclusion and the publisher policy
are enforced in code, not left to instructions.

### 8.2 Goal integration queue

For each eligible task:

1. Select its verified output and the current goal tip.
2. Construct a candidate integration in a controlled workspace.
3. Run required checks against the candidate; run relevant reviews.
4. Advance the goal only if the validated inputs remain current.
5. Persist an integration receipt before releasing dependent tasks.

Serialize updates per goal repository. Head/base changes invalidate affected
evidence. Conflicts become bounded repair attempts. Unknown verification holds
integration rather than being interpreted as success.

Add explicit goal/task base and target fields; do not temporarily change the
project's default_branch.

### 8.3 Stack goals

Use one goal branch per participating repository and a manifest of verified
commit SHAs across the stack. Existing labels still determine which member
repositories each task needs.

Release dependencies only when the required verified outputs are included in
the task's actual starting repository combination. A Linear Done state or
unqualified PR-merged flag cannot establish that.

Run cross-repository acceptance against the manifest. Multi-repository updates
are not atomic: journal partial integration and keep dependent work held until
the complete revision is verified. The final deliverable contains linked PRs,
compatibility constraints, and a human merge order.

## 9. Budgets, steering, and visibility

Attribute coordinator, worker, reviewer, validation, and repair usage to the
goal. Reserve capacity before starting new work. Existing dispatch-only budget
holds are insufficient for a long-running goal.

Bound model calls, task attempts, review rounds, total tasks, and elapsed runtime.
Where usage reporting is delayed, expose estimates and uncertainty rather than
claiming exact spend enforcement. Preserve work when stopping a worker.

Pause stops new dispatch and integration. Stop-workers is a separate action
that checkpoints/preserves output. Cancellation retains audit history and
recoverable work; it is not an automatic destructive cleanup.

Linear shows the brief, tasks, decisions, blockers, verified summary, and PRs.
Lola shows those plus live sessions, attempts, costs, evidence, and controls.
Both link to each other. The user should not need a second backlog in Lola.

## 10. Implementation sequence

| Phase | Deliverable | Exit condition |
| --- | --- | --- |
| 0. Visible goals, no autonomous execution | Existing-project binding, parent issue, optional milestone, task/dependency preview, routing validation, durable sync | Work is understandable in Linear; partial creation/retry cannot duplicate or prematurely dispatch it |
| 1. Workspace learning | Candidate capture, provenance, scoped activation, retrieval, LEARNINGS.md export proposal | A verified correction survives sessions and reaches relevant work without contaminating unrelated tasks |
| 2. Specialist profiles | Backend and UI/UX profiles first, structured findings, independent guards, artifact access | Same-provider profiles remain independent; failed/incomplete review cannot appear clean |
| 3. Bounded single-repository goals | Coordinator, task/attempt store, isolated workspaces, verification, goal integration | Main remains unchanged and forbidden-target operations are rejected |
| 4. Stack goals | Label-based member selection, per-repository branches, manifest, combined acceptance | Tasks receive the correct verified repository combination and partial integration is recoverable |
| 5. Measured improvement | Lesson maintenance, reviewer calibration, proposed checks/skills | Lower repeat-error or intervention rate at an acceptable cost |

The first autonomous pilot should use a small bounded goal, modest concurrency,
explicit acceptance checks, and a final human-only PR. It should not require
initiatives, new Linear projects, a vector database, or a framework replacement.

## 11. Implementation boundaries and tests

Likely ownership:

- internal/linear: milestone lookup/create, issue milestone field, goal binding
  reads, and recoverable update seams; extend the fake alongside the API.
- internal/planner: structured, repository-informed task proposals and static
  validation. Keep existing plan/apply semantics.
- New goal/knowledge/profile domain packages: pure records and rules where
  possible; no dependency on TUI or desktop.
- internal/daemon: durable scheduling, reconciliation, Linear projection,
  background jobs, integration authorization, and external-effect journal.
- internal/runtime/worktree/sandbox: isolated goal and attempt workspaces,
  explicit bases, briefings, and restart/adoption metadata.
- internal/config: static settings and inheritance only; runtime access checks
  remain outside config.
- internal/protocol, TUI, desktop: goal views and actions with parity.

Required verification:

1. Partial Linear provisioning, lost create responses, and repeated apply/retry.
2. Dispatch exclusion under label, state, and label-free polling.
3. Parent exclusion after all children finish.
4. Correct per-task routing, including always-included stack members.
5. Human edits, canceled dependencies, deleted bindings, and sync outages.
6. Restart after spawn/integration succeeds but before its outcome is recorded.
7. Stale review head/base, unavailable checks, malformed coordinator output.
8. Profile independence and backward-compatible provider/guard handling.
9. Goal PR exclusion from the existing merge queue; wrong-target write denial.
10. Multi-repository partial integration and coherent manifest recovery.
11. Budget exhaustion, pause/cancel, and preservation of dirty work.
12. Evidence-backed learning promotion, conflicts, scope, and retrieval limits.

Use the repository's Makefile checks when implementing. Build, vet, and the full
Go suite passed for the initial goal/learning/profile foundation; rerun relevant
checks as the remaining implementation lands.

## 12. Evaluate whether Lola actually improves

Track repeat corrections, useful versus dismissed findings, regressions caught
before human review, human intervention time, and cost per accepted task.
Compare representative tasks with and without selected lessons/reviewers under
comparable budgets. Preserve model, prompt, rubric, and lesson versions.

Implementation: `lola compare template`, `prepare` and `report` provide a
versioned portable JSON record and deterministic JSON/Markdown reports. Each
predeclared task/repetition binds its brief, starting commits, acceptance and
finite budgets; runs retain worker/reviewer model and prompt provenance, rubric
versions and exact lesson versions/content digests. All required outcome metrics
must be present, with explicit unavailable values when needed. Reports preserve
missing/over-budget pairs, coverage and model/prompt confounders; failed work is
included in cost per accepted task. These are recorded assessments, not an
independent certification or causal claim. The workflow and metric definitions
are in [comparison records](docs/comparison-records.md). Focused domain/daemon/CLI
tests, a domain race check and full `make check` (build, vet and tests) pass
(`/tmp/lola-comparison-make-check.log`).
Representative comparisons have not yet run.

More notes, agents, tasks, or review comments are not success metrics. More
verified progress with less repeated correction is.

## 13. Decisions and remaining questions

Confirmed direction:

- Keep Nori and its repositories in the existing Nori Linear project.
- Continue using task labels for repository selection.
- Leave initiatives untouched.
- Keep autonomous work isolated from main; final merging is human-only.

Recommended pilot decisions:

- Use a parent issue for the goal brief.
- Offer a goal milestone for new tasks; preserve existing release milestones
  when adopting work already assigned to them.
- Make Linear visibility and recovery the first phase.
- Keep execution facts and permission policy in Lola.

Pilot brief supplied by the user (2026-10-21): improve dialog layouts so they
fit the current visual style, with a maximum runtime of 30 minutes. The question
named Nori; use Nori as the project assumption and confirm it against the current
workspace before publication. The user did not choose an agent-call count, so the
initial implementation choice is a ceiling of 8 total calls, including planning,
workers and specialist reviews, with one worker at a time. Keep the pilot to a
bounded representative dialog flow after inspecting the existing design and
dialog inventory. Preserve behavior and keyboard/focus access, and retain
before/after and narrow-layout evidence. Establish the exact task scope and
available acceptance checks from that inspection before starting the 30-minute
execution. The first live run and its remaining verification requirements are
recorded below; the original brief alone grants no permission to bypass them.

Read-only pilot discovery: the local Lola configuration maps `nori-app`
(`sushidev-team/nori-app`) and `nori-website` to the same existing Nori Linear
project. Nori App contains PRODUCT.md, DESIGN.md, a shared
`resources/js/components/ui/dialog.tsx`, dialog stories and dialog tests, as well
as many feature dialogs. Inspect those shared primitives and representative
stories first when bounding the pilot; no Nori source or runtime configuration
has been changed by this discovery.

Concrete pilot candidate from read-only source inspection (2026-10-22): align
the shared dialog presentation with Nori's existing design tokens and validate
it through `ui/dialog.stories.tsx` plus the invoice **Save view** dialog. The
current shared overlay still uses `bg-black/80`, while DESIGN.md's
Legible-Scrim Rule specifies `overlay/scrim` (36% steel ink in light mode, 60%
black in dark mode). Shared content also uses legacy `shadow-lg` rather than the
documented dialog elevation. These are observed source mismatches; the pilot
must inspect the actual rendered themes before choosing changes.

Keep implementation ownership to `resources/js/components/ui/dialog.tsx`, its
story/test files, and, only if layout requires it, `Invoices/SaveViewDialog.tsx`
and its existing test. Preserve Radix behavior, viewport gutters, `dvh` height
limits, long-content scrolling, close-button visibility and caller overrides.
Do not change saving/filter payloads, backend behavior, design tokens or other
feature dialogs. Acceptance should include light/dark desktop and narrow
before/after captures, tall-dialog scrolling, keyboard dismissal/focus return,
and passing the existing dialog and save-view tests. Available repository
commands include `npm run test:unit -- resources/js/components/ui/__tests__/dialog.test.tsx resources/js/components/Invoices/__tests__/save-view-dialog.test.tsx`,
`npm run test:storybook -- resources/js/components/ui/dialog.stories.tsx`, and
`npm run build`. The sandbox and baseline checks are now established in the
follow-up evidence below. No Nori source file or external object was changed
during preparation.

Read-only live readiness check (2026-10-22): Linear confirms the existing Nori
project (`9a5df54f-9fb1-4628-9dff-f9fc7d84b795`) and team
(`ace69aca-dd39-4c63-91dc-36bbf48b62c7`). Team workflow states include In Progress
(`9c45fbca-2609-4631-8980-9db38b5c6d3d`), In Review
(`683c0e02-3037-40a1-a373-b7a53a88ab01`) and Done
(`bd4bbeec-f062-4151-8f1c-e52569490ba7`). Existing milestones are First Public
Release and Hypercare; no milestone or issue was created. Docker responds
(version 29.8.2), but the local Lola config has no sandbox image/Linux binary or
Nori goal-mode configuration. The current shell has no supported Claude/OpenAI
model credential environment variable; container-compatible authentication still
needs establishing. No secret values were read or printed. GitHub/Linear PR
state automations remain unverified. These checks do not start the pilot clock.

Follow-up preparation (2026-10-22): the Linux Lola build and a local digest-pinned
Nori image are ready. The unchanged source at
`97fc399c591d82cfcf54859fa0c0191311cb16b9` passes 10 dialog/invoice unit tests,
3 dialog Storybook tests and the frontend build in an offline disposable
workspace. Four light/dark desktop/narrow browser cases pass keyboard/focus,
gutters, synthetic overflow and close-visibility checks with verified local
fonts. The rendered baseline confirms the scrim/elevation mismatch, and the
new style gate rejects it as expected. Lola's real Docker validation isolation
and timeout-cleanup test passes against the final image. Exact inputs, logs,
screenshots, setup findings and limitations are retained in
[the pilot evidence](docs/evaluation/nori-dialog-pilot/README.md).

The feature build's comparison commands were exercised through a separate live
daemon socket with ordinary polling disabled; the unrun pair reports missing
outcomes explicitly. The user then supplied the isolated model credential and
the bounded pilot started on 2026-10-22 local time. Linear now has parent NOR-493,
task NOR-494 and the new Dialog layout pilot milestone in existing Nori, with
apps/app routing and no agent-ready label. Team PR automations were inspected;
Lola performs local task integration and only publishes final draft PRs.

The live run discovered and fixed generated Markdown normalization and a fresh
Claude workspace trust prompt. Publication recovered using the same reserved
IDs. An active-worker daemon restart recovered the same attempt and existing
two model calls without launching a duplicate. Worker unit/story/build/browser
checks passed, then the observed-token budget stopped the worker before
submission (285k including cached input against the selected 250k ceiling).
The output is preserved; independent verification, specialist review and final
delivery remain unfinished. A fresh authorization with a cache-aware limit was
proposed to the user, retaining the aggregate time/call limits. Exact authorization, times, recovery evidence and
limitations are retained in [pilot evidence](docs/evaluation/nori-dialog-pilot/README.md).
No final pilot-success or comparison-improvement claim is made yet. The normal
installed daemon is unchanged; all feature execution uses the isolated pilot home.


### Continuation checkpoint — 2026-10-22

The approved continuation reached worker submission and an independent verification
failure: unit/story/browser checks passed, but the pinned 2 GiB verifier exhausted
Node's default heap during the build. A 1792 MiB heap passed a separate build
under the same boundary. A final recovery worker passed all checks but its
three-minute attempt deadline stopped it before submission. Five aggregate model
calls, 1,224,631 observed tokens and ~$0.806 estimated spend were recorded; no
integration, specialist pass or PR occurred. See the retained
[pilot evidence](docs/evaluation/nori-dialog-pilot/README.md).

Acceptance checks now support bounded, policy-bound memory limits in desktop,
TUI, Docker execution and usage reporting. Existing authorizations preserve their
2 GiB limit and digest. The normal installed daemon remains unchanged.

Remaining: complete one adequately timed end-to-end pilot after exact-boundary
preflight, then run representative comparisons. The pilot also identifies a
follow-up UX need: renew a run through an explicit new authorization under the
same goal/Linear parent, preserving earlier runs and budgets as immutable history.

### Handoff-device preflight — 2026-10-22

The sandbox was reconstructed on the handoff device with matching dependency
and harness input hashes and freshly built host/Linux ARM64 Lola binaries.
The complete corrected-heap acceptance command passed in 95.386 seconds under
the exact offline 2 GiB boundary on a disposable historical recovery candidate:
11 unit tests, 3 Storybook tests, build and four browser cases. Lola's real
container boundary/timeout-cleanup test also passed. See
[retained environment evidence](docs/evaluation/nori-dialog-pilot/handoff-preflight/).

No new goal, model call or live execution has started. Secure local Linear and
Claude authentication is missing; the already-approved additional 20-minute run,
eight-minute worker attempt and three calls remain available under the unchanged
aggregate token/spend ceilings. This completes the new-device environment
preflight, not the live-pilot or representative-comparison requirements.
