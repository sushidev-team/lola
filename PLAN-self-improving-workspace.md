# Self-improving workspaces and autonomous goals in Lola

Status: implementation paused at the user’s request on
`feat/self-improving-workspaces` (2026-10-21). The full plan is not complete.
Approximate scope estimate: 75% implemented, not a measured completion metric.

Checkpoint verification: `make check` passed (format, build, vet and full Go
suite); focused finding-lifecycle and daemon race tests passed. Frontend
`npm run check`, full `npm test`, and `npm run build` passed for the current UI;
the final copy adjustment also passed the focused ProjectForm suite and build.
Desktop/narrow fixture screenshots and scoped UI review are retained under
`.impeccable/review/`. No live end-to-end pilot has been run.

### Checkpoint and quick user testing

This checkpoint is intended for a quick UI/CLI look and development testing.
The source builds and the automated suites run against fakes and temporary
repositories. No live autonomous pilot, deployment, main-branch merge, or installed
daemon replacement has been performed. UI screenshots use clearly labeled fixtures.

Available for inspection: project/stack Execution settings; goal overview,
tasks, limits, delivery and steering controls in desktop/TUI; CLI goal planning,
authorization and recovery; knowledge capture/curation/export; specialist reviews
and finding decisions. Goal authoring and authorization remain CLI-first.

Remaining before full-plan completion:

- Goal creation/authorization forms and remaining desktop/TUI action parity.
- Broader task/policy revision reconciliation and Linear steering beyond the
  implemented brief clarifications and bounded task replanning.
- Learning maintenance, referenced-code revalidation, recurring-error proposals
  for tests/lint/skills, and evidence-backed promotion beyond current curation.
- Worker-originated specialist disputes and richer calibration, including model
  and prompt provenance and comparable-budget outcome measurement. The current
  finding report counts explicit operator decisions; it is not an accuracy score.
- A bounded live pilot, representative comparisons, and a requirement-by-requirement
  completion audit, including optional parent human acceptance and cross-repository
  delivery guidance.

Quick-look setup and exact current commands are in
[the implementation guide](docs/workspace-goals.md#quick-look-at-this-checkpoint).
Resume from this list; green checks at this checkpoint do not complete the plan.

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
The full plan is not complete. Broader task/policy revision reconciliation,
authoring/learning/profile forms, remaining UI parity
and evaluation remain. See
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

Resolve before enabling execution, through configuration and a concrete pilot:

- Which existing team states represent active work and review?
- Which GitHub/Linear automations currently change issue state on intermediate
  PR events?
- Which checks and artifacts establish acceptance for the pilot?
- Which finite limits and authorized actions apply?

No Linear objects, repository configuration, or runtime behavior were changed
while researching or writing this proposal.
