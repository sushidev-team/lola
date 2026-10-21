# Workspace goals, learning, and specialist reviews

Implementation is in progress. The commands below are available on the feature
branch. Bounded execution and inspection controls are implemented; the full
plan, remaining authoring forms and evaluation are still in progress. Publishing
a goal creates its Linear representation without starting workers.

## Goals in existing Linear projects

Configure `team_id` and `project_id` on the Lola project or stack. For a stack
such as Nori, keep one Linear project and use the existing member routing labels.
No initiatives or Linear projects are created.

### Make goal mode available

In the desktop project or stack settings, open **Execution**. Select **Docker
workspace**, supply an installed container image (or use `[sandbox].image`),
and turn on **Make autonomous goals available**. Choose the existing Linear team
and project under **Issue pickup** first. In the TUI editor, the same controls
appear on the Repo tab as **Workspace isolation**, **Container image** and
**Autonomous goals**. Stack members use their owner's execution settings.

Docker setup also requires `[sandbox].lola_bin`, an absolute path to a Linux Lola
build. See [the container sandbox](../README.md#the-container-sandbox) for the
machine setup. Configuration validation reports missing setup without saving a
partial change. Switching back to Host workspace requires turning goal mode off
and clearing any sandbox image override or configured sandbox ports.

This setting makes goals available; it never starts or authorizes one. Each goal
still needs its reviewed task plan, finite execution policy and explicit `goal
start` authorization. Existing sessions retain the workspace they started in.

### Ask the coordinator for a task proposal

Enable `goal_mode = true` and `sandbox = "docker"` on the project or owning
stack, with an installed sandbox image containing `sh`, `timeout`, and its
configured coding agent. Then supply an outcome without writing the task graph:

```json
{
  "id": "6cb0ae02-b741-4bb4-9b08-9e72007456cb",
  "project": "nori",
  "title": "Team invitations",
  "outcome": "An administrator can invite a colleague to the team.",
  "acceptance": ["The recipient can accept an unexpired invitation."],
  "non_goals": ["Organization billing"],
  "max_tasks": 10,
  "timeout_seconds": 180,
  "context_paths": {
    "nori-api": ["README.md", "src/invitations.ts"],
    "nori-web": ["README.md", "src/routes/team.tsx"]
  }
}
```

Use real configured member names and existing committed files. Choose a fresh
UUID for a new paid proposal; retry the same UUID to inspect the original result.
If `id` is omitted, the CLI prints a generated ID before contacting the daemon.

```sh
lola goal propose brief.json
lola goal proposal PROPOSAL_ID
lola goal accept-plan PROPOSAL_ID --revision REVIEWED_PROPOSAL_REVISION
```

`propose` makes one bounded model call and saves its input, output, source
commits, selected lessons, acceptance coverage, unresolved questions, and reserved
goal identity. It never creates Linear issues or starts workers. `accept-plan`
creates a local goal only. Continue with `goal publish` and a separately reviewed
`goal start` authorization below. The accepted planning call counts toward that
goal's agent-run budget.

The planner receives a limited snapshot of each configured repository's local
delivery branch. Uncommitted work is excluded. By default, context includes
available README/AGENTS files and common build manifests; `context_paths`
replaces that default for the specified member. Each file is bounded to 16 KiB,
with at most 20 selected files per member and 512 KiB of total model input. A
bounded file listing provides orientation. Conventional secret files, symlinks,
binary files, and Git internals are excluded; this is not a general secret scanner
for credentials embedded in ordinary source files. Requested excluded, missing,
or oversized files cause a clear error. The saved proposal shows what was read.

The planner runs in a separate container with no host filesystem or socket mounts,
read-only root, bounded resources and lifetime, and a temporary agent home.
Only configured model credential names from Lola's strict allowlist are forwarded;
GitHub, Linear, SSH, and arbitrary project environment are excluded. Network
access is available for the model API. The image is not pulled automatically.

Lola validates task dependencies, goal-criterion coverage, and actual label-based
stack routing. Coverage is a planning claim, not proof of implementation. A model
cannot replace the outcome, acceptance, non-goals, or execution policy. Accepting
requires the reviewed proposal revision and unchanged repository commits and
routing. Failures, malformed output, interrupted calls, and uncertain container
cleanup never trigger an automatic retry. A new proposal ID requests a new call.
If you want to edit the tasks manually, copy the proposed draft into the
`goal create` format below; the original proposal remains inspectable.

### Define the task graph directly

Create a goal brief, for example `invitations.json`:

```json
{
  "project": "nori",
  "title": "Team invitations",
  "outcome": "An administrator can invite a colleague to the team.",
  "acceptance": ["The recipient can accept an unexpired invitation."],
  "non_goals": ["Organization billing"],
  "milestone_name": "Team invitations",
  "tasks": [
    {
      "title": "Implement invitation API",
      "description": "Add invitation creation and acceptance.",
      "acceptance": ["Expired and reused tokens are refused."],
      "apps": ["nori-api"],
      "paths": {"nori-api": ["src/invitations", "tests/invitations"]},
      "label_ids": ["REPLACE_WITH_EXISTING_API_LABEL_UUID"]
    },
    {
      "title": "Implement invitation form",
      "acceptance": ["The form works with keyboard navigation."],
      "apps": ["nori-web"],
      "paths": {"nori-web": ["src/routes/team", "tests/invitations"]},
      "label_ids": ["REPLACE_WITH_EXISTING_WEB_LABEL_UUID"],
      "blocked_by": [1]
    }
  ]
}
```

Use actual configured project/member names and Linear label UUIDs. `apps` must
match the members those labels select, including members without routing labels
(those are included for every task). A single-repository goal can omit `apps`.
Dependencies use one-based indexes of earlier tasks.

`paths` declares file or directory prefixes for every routed repository. It is
optional for manually defined tasks; omission inherits the accepted execution
policy's member scope. New coordinator proposals must declare it explicitly.
An empty array for a member means read-only context, useful for always-included
stack members. For example, `"paths": {"api": ["src/invitations"], "shared": []}`
permits API changes while exposing `shared` read-only. Every worker task needs
at least one writable deliverable. These are literal prefixes, not glob patterns;
Git/Lola control directories, escaping paths, and ambiguous path forms are refused.

Task ownership can only narrow the accepted goal policy. Lola records the resolved
scope on each attempt before launching it, includes it in the worker brief and
Linear task description, and mounts read-only context repositories read-only.
Write-scoped repositories remain private writable workspaces; the coordinator
rejects submissions that change anything outside the recorded task paths,
including both sides of a rename. Such a rejection can schedule a repair within
the original attempt/run limits. It cannot expand the task scope.

Tasks with overlapping write prefixes wait for the active attempt to finish
verification. Disjoint scopes may run concurrently within the accepted caps;
read-only context does not acquire a write lease. Older attempts with no stored
scope resolve conservatively from their accepted task and policy. Existing task
contracts are retained; a coordinator cannot silently replace their acceptance
criteria or ownership while work is running.

```sh
lola goal create invitations.json
lola goal show GOAL_ID
lola goal publish GOAL_ID
lola goal list
```

`create` validates placement and saves local reservations. `publish` creates the
parent, optional milestone, children, and blocking relations. Repeating it
reconciles the same IDs after an interrupted request. It never creates another
copy of a confirmed object that somebody deleted or moved.

Omit both milestone fields for parent-only grouping. Use `milestone_id` instead
of `milestone_name` to retain an existing release milestone. Existing milestone
names and descriptions are preserved.

To adopt existing work, set `existing_issue_id` to its Linear UUID on a task in
the draft. Copy its current `title`, `description`, and `label_ids` exactly;
provide the goal-specific `acceptance`, repository `apps`, and optional `paths`.
Lola checks that the issue is active, unfinished, in the configured team/project,
and not already owned or running. For example, a task can contain:

```json
{
  "existing_issue_id": "12ab5678-1234-4234-9234-123456789abc",
  "title": "Invitation API",
  "description": "Existing issue text copied without changes.",
  "label_ids": ["existing-api-label-uuid"],
  "apps": ["nori-api"],
  "acceptance": ["Expired invitations are rejected by the API"]
}
```

Adoption preserves the existing issue's parent, release milestone, text, labels,
and workflow state. Publication creates a reserved `related` link from the goal
parent to the existing issue; its accepted criteria appear in the goal brief.
New tasks still become children of the goal and use its optional milestone.
The relation uses Linear's documented
[issue relation schema](https://github.com/linear/linear/blob/master/packages/sdk/src/schema.graphql).
Losing a response reconciles the same link. Removing a confirmed link or editing
the accepted issue causes a hold, never a replacement issue or a rewritten brief.
Coordinator proposals and autonomous replanning cannot select existing issues;
adoption requires explicit selection in the human-authored draft.

Existing blocking links between adopted tasks retain their original UUIDs.
Declare those prerequisites in `blocked_by`; an unfinished prerequisite outside
the accepted graph holds adoption/execution. Finish external work or include it
explicitly before proceeding. An existing live session or open PR also holds
adoption. Older native sessions cannot be revived or auto-merged after their
issue has been reserved by a goal.

Goal markers and durable issue reservations exclude the parent and tasks from
ordinary polling, reconciliation, manual ticket launch, and legacy plan/apply.
A published related-issue link also excludes adopted work in a different Lola
home. Relation reads that exceed the bounded page hold ordinary dispatch because
ownership is uncertain. Local adoption and worker claims are serialized, and
older native sessions cannot write lifecycle updates into a newly adopted issue.
A damaged goal store holds ordinary issue dispatch. Linear edits or unavailable
bindings surface as `sync_error`; they are not overwritten by a retry.

## Reconcile a human edit to a goal brief

Lola detects changes to the parent issue's title or description before new
execution effects. It retains the exact Linear text as a proposed revision and
holds new dispatch. It never parses discussion into permissions or silently
rewrites the parent. To refresh a paused or not-yet-authorized goal explicitly:

```sh
lola goal sync-brief <goal-id>
lola goal show <goal-id>
```

Review `brief_sources`, `pending_brief`, the existing task graph and the accepted
policy. Then supply an explicit structured interpretation of that exact source:

```json
{
  "id": "<goal-id>",
  "revision": 42,
  "source_id": "<pending_brief hash>",
  "contract": {
    "title": "Invitations",
    "outcome": "Invite a colleague with keyboard access",
    "acceptance": ["Recipient can accept", "The invitation flow is keyboard accessible"],
    "non_goals": ["Changing authentication providers"]
  },
  "reason": "The retained API and UI tasks cover the clarified outcome; repeat final verification."
}
```

```sh
lola goal accept-brief revision.json
lola goal show <goal-id>
lola goal resume <goal-id> --revision <new-reviewed-revision>
```

Acceptance re-reads Linear, checks every task/dependency binding and rejects a
concurrent edit or stale local revision. It records the before/after contract,
source hash and reason. An authorized goal remains paused after acceptance;
acceptance itself launches nothing. Reverting Linear to the accepted text and
running `sync-brief` clears the pending proposal without deleting its history or
resuming work. Repeated observations of the same text share one source snapshot.

The retained task contracts, repository routing, execution policy, deadlines and
usage budgets do not change. Stop or finish active attempts before acceptance;
pending integration/replanning must first settle. Old final checks and artifact
links move into the accepted revision's history and remain in usage accounting.
A fresh whole-goal review is required, consuming the original review-round budget.
An interrupted check/reviewer must be confirmed stopped before new evidence can
be reserved. A publication journal, target refresh or existing delivery blocks
this reconciliation path: retain that delivery history and use follow-up work
for a changed contract after publication. Larger changes that require different
tasks, members, checks or permissions still require a separately reviewed plan
and authorization; this command cannot widen them.

Snapshots are bounded to 64 distinct sources (128 KiB per description) and 64
accepted revisions. Parent ownership/placement changes and task edits remain
holds, not acceptable brief text. Priority changes and discussion comments are
not yet interpreted by this revision workflow.

## Progress in Linear

Authorized goals publish coalesced progress comments on their existing parent
issue. Updates show execution/hold state, integrated versus superseded tasks,
accepted brief and task-plan revision counts, whole-goal verification, and final
PR/merge observations. Integration is explicitly distinguished from delivery.
Lola leaves human descriptions, discussion, labels, milestones and workflow
states untouched. A changed brief is a decision to review, never a verified goal.

The durable outbox reserves a UUID and exact observed summary before sending.
Linear's [current public schema](https://github.com/linear/linear/blob/master/packages/sdk/src/schema.graphql)
supports an explicit comment UUID and filtered comment lookup; Lola uses these
to recover lost responses. Each comment includes the observation time. Unchanged
facts produce no comment; the two-minute interval coalesces rapid transitions.
Heartbeats, token counters and raw tool errors are excluded. Each goal retains at
most 512 updates. The controller's narration pass has a ten-second total deadline
and a three-second per-goal deadline, separate from execution authority.

If an attempted send cannot be found, Lola holds that update instead of recreating
a potentially deleted comment. Edited, moved or archived comments also remain
untouched. `goal show` exposes `progress_updates` and `progress_error`; failure to
publish progress does not pause workers or change verification. After reviewing
an uncertain update in Linear, dismiss it locally if it should be left as-is:

```sh
lola goal dismiss-progress <goal-id> --revision <reviewed-revision> \
  --update <pending-update-uuid> --reason "The comment was deliberately removed"
```

Dismissal retains the send intent and reason without claiming successful delivery.
It permits a later meaningful update; it does not repost the dismissed summary.
Disabling goal mode or moving the configured project holds further narration.
No initiatives are created or modified. Comments do not mark tasks shipped;
child workflow transitions use the separate verified lifecycle below.

### Child issue workflow states

Goal mode reuses the owning project/stack's existing workflow settings. Empty
settings cause no state mutation; Lola creates no new workflow states.

| Existing setting | Goal task trigger | Required Linear state type |
| --- | --- | --- |
| `on_spawn_state_id` | A worker is running or has submitted work | `started` |
| `on_pr_state_id` | The task has verified goal-branch integration | `started` |
| `on_merged_state_id` | The entire goal has observed target-branch delivery | `completed` |

Lola reads the configured team's states before each transition. A Done/Canceled
state configured for integration is refused. A reserved worker alone is not
started work. Superseded tasks retain their own history and are never marked
shipped through replacement completion. Pause, cancellation, brief changes and
scope holds suspend new workflow effects. Labels, text, parents and milestones
are not part of these state writes, including for explicitly adopted issues.

Each transition records task, lifecycle stage, source/target states, reservation,
send and confirmation times in `goal show`'s `workflow` history. The source state
is re-read immediately before sending. A lost response is confirmed by observing
the intended target; it does not cause another write. If a sent transition is
unconfirmed or a human chooses another state, Lola preserves that choice and
reports `workflow_error`. Inspect the journal and reconcile the intended state
in Linear; a later observation can acknowledge it. Changing configuration cannot
silently replace an outstanding transition. A held task's workflow projection
does not prevent other eligible tasks from being synchronized.

Linear's state update has no compare-and-set field in the
[public issue-update schema](https://github.com/linear/linear/blob/master/packages/sdk/src/schema.graphql).
The final read narrows the concurrent-edit window but cannot make a remote
read/write pair atomic. Observed edits are preserved, and uncertain writes are
never blindly repeated. Linear Done does not establish integration: marking an
unverified task completed holds new execution until reconciled. Existing verified
integration and target-branch evidence remain Lola's authority for dependencies
and delivery.

## Workspace learning

Every newly launched agent receives instructions to capture reusable discoveries
in `.lola/learnings.json`. At turn end Lola reads at most 64 KiB and 20 entries,
records the session source, and stores them as candidates. This works through
the existing stop callbacks across coding agents. Worker-supplied `status` or
verification claims do not activate lessons.

An entry has this form:

```json
{
  "topic": "Durable dispatch",
  "summary": "Persist ownership before starting an external operation.",
  "rationale": "A restart must not dispatch duplicate workers.",
  "paths": ["internal/daemon"],
  "tags": ["persistence", "dispatch"],
  "evidence": ["Regression test for interruption between reservation and spawn"]
}
```

For a human correction, wrap that entry in a file containing `scope` and
`proposal`:

```json
{
  "scope": {"project": "lola"},
  "proposal": {
    "topic": "Build commands",
    "summary": "Use the repository Makefile so Go uses the local build cache.",
    "rationale": "The sandbox cannot write to the global cache.",
    "evidence": ["Explicit project maintainer correction"]
  }
}
```

```sh
lola learn correct correction.json
lola learn list --project lola
lola learn lookup --project lola --path internal/daemon/dispatch.go
lola learn accept LESSON_ID --version 1 --evidence "Reproduced in test XYZ"
lola learn dispute LESSON_ID --version 2 --evidence "This no longer applies after migration XYZ"
lola learn export --project lola --output ./learning-proposal
```

Explicit corrections activate when they are scoped and do not conflict with
existing guidance. Technical discoveries need acceptance evidence. Resolving a
conflict requires naming the replaced IDs with `--supersedes`. Policy changes
remain proposals. Environment-specific lessons can receive an RFC3339
`--expires` timestamp on acceptance.

Retrieval excludes candidates, disputes, superseded entries, expired lessons,
and unrelated projects/paths/goals. Launch briefings contain at most 8 KiB of
selected lessons and record their exact versions. Path-specific lessons are
available through explicit lookup and specialist review retrieval when affected
paths are known. Export creates a new directory containing a `LEARNINGS.md`
index and focused lesson files for ordinary code review; it does not edit an
existing repository documentation file.

## Specialist profiles

Profiles select an existing agent-family provider and supply a versioned rubric.
Several profiles can use the same provider independently:

```toml
[[review.provider]]
provider = "codex-session"
enabled = true

[[review.profile]]
id = "backend"
kind = "backend"
provider = "codex-session"
version = "1"
enabled = true
on_pr_open = true
required = true
projects = ["lola"]
paths = ["internal"]
timeout_seconds = 300
max_rounds = 3
```

Supported specialties are `backend`, `uiux`, `security`, `integration`, and
`performance`. Optional `rubric` adds project-specific criteria. `paths` are
repository-relative prefixes, not globs. Profile execution is currently
restricted to agent-family providers because it requires rubric instructions
and structured output; existing CLI and bot-watch providers retain their
existing behavior.

```sh
lola profile list
lola profile run SESSION_ID --profile backend
lola profile results
```

The default acceptance contract comes from the linked Linear issue. For a manual
session, provide `--contract brief.md`. Reviews run through the bounded review
worker and return a queued acknowledgment. Use `--retry` for an incomplete or
failed result within the attempt cap.

Verdicts bind to the profile, rubric, repository, head and base commits,
acceptance contract, lessons, and artifact manifest. Missing evidence, malformed
output, provider errors, dirty source, and changed commits cannot produce a pass.
Required profiles hold Lola's merge queue until satisfied. UI/UX requires
screenshots and interaction evidence; integration and performance require
their respective verification artifacts. The disposable validation engine supplies those artifacts to goal reviews,
with hashes rechecked before integration. Ordinary PR reviews without the
required runtime evidence still hold as incomplete.

## Execution engines under development

`internal/goalwork` creates independent repositories for goal branches and each
worker attempt. Workers submit a bounded Git bundle; host Git never reads their
writable Git configuration. Candidate integration checks the assigned starting
commit and allowed paths. Only the goal branch can advance, with an exact-base
compare-and-swap and a durable receipt. A stack operation records partial
updates and becomes complete only after every member is acknowledged against
the verified commit combination. Original checkouts and main remain untouched.

`internal/goalcheck` runs accepted commands on fresh copies of that combination.
Checks require an installed image identified by its SHA-256 digest and a bounded
timeout. The container has no network, host credentials, or Docker socket, and
only its disposable workspace is mounted writable. Evidence is copied into a
daemon-owned directory after the container stops, with size limits and content
hashes. Missing artifacts, changed commands or revisions, and interrupted runs
cannot satisfy acceptance. Container controls use Docker's documented
[run options](https://docs.docker.com/reference/cli/docker/container/run/).

`internal/goalreview` runs goal specialists in a separate container using the
owning project's `sandbox_image`, falling back to `[sandbox].image`. The local
image is resolved to its exact ID before launch and is never pulled. It must
provide the selected provider's CLI, `sh`, `env`, `mkdir`, `git`, and GNU
`timeout` on PATH, usable under the daemon's numeric UID/GID. Provider model
selection remains independent of the worker's agent.

Only the owned candidate and bounded copies of captured acceptance artifacts are
mounted, both read-only. Artifact hashes are checked before copying and host paths
are replaced with container paths. The root filesystem is read-only, disposable
configuration lives in a capped tmpfs, and CPU, memory, process count and runtime
are bounded. There is no mount of upstream repositories, host home, Docker socket,
or worker configuration. Network access is available for model APIs; only
explicitly permitted model credentials from `[sandbox].pass_env` are forwarded.
GitHub, Linear and SSH credentials are excluded, and image-defined environment
variables are cleared before the model CLI starts.

A durable receipt records the input key, provider/model and image ID before
launch. Results count only after container cleanup is confirmed. The daemon
rechecks that receipt before integration and publication. Interrupted calls keep
their reservations and evidence; `retry-verification` confirms old containers
have stopped before reserving new calls within the accepted budget. An old host
review result cannot satisfy this isolation requirement. Ordinary session reviews
retain their existing provider adapter. These controls use Docker's documented
[container options](https://docs.docker.com/reference/cli/docker/container/run/)
and [image inspection](https://docs.docker.com/reference/cli/docker/image/inspect/).

The goal controller authorizes, prepares, launches, observes and stops private
workers. Submitted bundles become owned candidates, then independent acceptance
checks and required specialist reviews run against the exact repository
combination. Only a complete durable integration releases dependent work. A
separate final verification checks all repositories and reviews the complete
diff from the original starting commits; `ready` means a local verified artifact
awaits human delivery. With `publish_pr = true` in the accepted policy, Lola
publishes the reserved goal branches and creates draft PRs after rechecking the
final evidence. It never merges them.

Concrete command failures, merge conflicts and actionable specialist findings
can schedule a new worker attempt within the accepted limits. Its briefing
includes the prior failure evidence. Unknown outcomes and missing evidence hold
execution instead. Every previous attempt, check and review is retained.

Focused tests run with `make test`. An optional real-container boundary test uses an
already installed image containing `sh` (no pull occurs):

```sh
LOLA_GOAL_CHECK_IMAGE=sha256:YOUR_LOCAL_IMAGE_ID \
  GOCACHE=$PWD/.gocache GOFLAGS='-mod=mod -buildvcs=false' \
  go test ./internal/goalcheck -run TestDockerValidationBoundary -v
```

The test mounts only generated temporary repositories and removes only its own
test containers. It also verifies timeout cleanup.

The durable execution model also records accepted policy digests, exact starting
manifests, attempts, reserved agent runs, and pending integrations. Reservations
respect dependencies, concurrency, attempts, elapsed runtime, and reported usage.
Pausing holds dispatch and integration; canceling retains the issue reservations
and all work records. Reported token/spend limits remain subject to usage-report
delay. Run counts are reserved before launch; deadlines are recorded for the
controller's worker-stop enforcement.

`[[project]].goal_mode = true` is an explicit, non-inherited availability setting
requiring Docker sandboxing and an existing Linear team/project. Stack members
use their owning stack's setting. This setting alone starts no work.

An authorization JSON file contains the goal's `id`, current `revision` from
`goal show`, and an explicit `policy`. Its policy has `members` (each member's
ID, configured project, absolute source checkout, GitHub repository, target
branch and optional allowed path prefixes), `checks`, required `profiles`,
`publish_pr`, optional `replan`, and finite `limits`:

```json
{
  "concurrency": 2,
  "max_tasks": 10,
  "attempts_per_task": 2,
  "review_rounds": 2,
  "target_refreshes": 1,
  "agent_runs": 30,
  "runtime_minutes": 120,
  "attempt_minutes": 30,
  "tokens": 500000,
  "spend_usd": 20
}
```

Worker images must already be installed and include the selected coding agent,
Git, `sh`, and `timeout`. The attempt deadline is also enforced inside the
container, so a stopped daemon cannot leave an unbounded worker. Host project
environment, symlinks, setup hooks and upstream credentials are not passed to
goal workers; only allowlisted model credentials named by `sandbox.pass_env`
are copied. Repository rules are still read inside the private checkouts.

```sh
lola goal start authorization.json
lola goal show GOAL_ID
lola goal pause GOAL_ID --revision CURRENT_REVISION --reason "Review progress"
lola goal resume GOAL_ID --revision CURRENT_REVISION
lola goal stop-workers GOAL_ID --revision CURRENT_REVISION
lola goal cancel GOAL_ID --revision CURRENT_REVISION
lola goal retry-verification GOAL_ID --revision CURRENT_REVISION --reason "Check environment repaired"
```

Start requires current Linear bindings and routing. Resuming rechecks them.
Canceled goals retain issue ownership and work files. Ordinary revive, agent
switching, host dev commands, checkpoints and mutable-worktree diffs cannot
bypass the goal controller. Final `goal/...` branches are excluded from automatic
merging, including when opened manually as ordinary sessions.

`retry-verification` is available on paused or blocked goals, within the accepted
review-round limit. It stops interrupted validation containers and reserves a
new operation identity without deleting failed evidence. An integration that
already recorded a ref-update intent must recover using that original operation;
it cannot be abandoned by retry. Pause cancels verification and prevents ref
updates. Stop-workers is separate and preserves submitted verification work.

Reported goal token/spend aggregation and UI controls
still need wiring before the autonomous pilot. Token/spend fields
currently express accepted limits; complete attribution and stopping on observed
usage are not yet available. Run counts and runtime/attempt deadlines are enforced.

### Bounded autonomous replanning

Set `"replan": true` in the accepted execution policy to allow the coordinator
to decompose a task after conclusive independent rejection. It is off when
omitted. An unknown check result, missing agent, permission question, or canceled
issue does not trigger decomposition.

The coordinator receives the original contract, verified repository combination,
bounded source excerpts from the task's owned areas, relevant scoped lessons, and
failure evidence. One isolated, time-bounded call proposes 2–10 smaller tasks.
Lola validates the result: children retain the original repository/label routing,
can only narrow write ownership, inherit original prerequisites, and collectively
cover every original criterion. Each child receives the exact original criteria
it claims to cover in addition to its proposed criteria. Model-reported unresolved
decisions hold execution. Mandatory checks, reviewers, outcome, non-goals, and
policy budgets remain unchanged.

The original task and attempts remain inspectable as `superseded`; they are not
marked integrated. Dependents unlock only when all replacement tasks have verified
integration receipts. Repeated decomposition is bounded by `max_tasks`, which
counts retained originals as well as children, and by the shared `agent_runs` and
runtime budgets. The final whole-goal verification still applies.

Child issue and relation IDs are saved before Linear writes. Partial publication
holds dispatch and resumes the same resources; it never creates a new goal,
milestone, project, or initiative. Child descriptions link their original task.
A restart or lost model response does not repeat a paid call automatically. Inspect
the operation ID under `execution.replans` with `lola goal proposal OPERATION_ID`.
If a reserved coordinator call was interrupted or rejected, explicitly request a
fresh charged call within the original budget:

```sh
lola goal retry-replan GOAL_ID --revision CURRENT_REVISION --reason "Retry interrupted coordinator"
```

Once replacement identities have been reserved for publication, use `goal resume`
after repairing the external failure. `retry-replan` refuses to abandon partial
publication. Pausing cancels the coordinator and prevents graph application and
subsequent writes; cancellation preserves every recorded attempt and issue.

## Publishing verified goals

Publication is authorized only by `publish_pr: true` in the accepted execution
policy. Each member must identify its exact GitHub repository and delivery
target. `internal/goalpublish` derives the only writable branch,
`goal/<goal-id>`, from the durable goal identity. It refuses a default-branch
push, forced update, unverified remote head, or changed PR target. If a delivery
target advances beyond the verified delivery revision, publication holds for a
refresh and new verification. Credentials
remain on the daemon side; no upstream credentials enter a worker.

`limits.target_refreshes` authorizes up to three automatic refreshes; omitted or
zero disables them and preserves existing accepted policies. When publication
observes a changed target, the controller reserves the full target combination,
archives the preceding final proof and delivery links, then fetches exact commits
into separate daemon-owned stores. It stages merges, runs independent acceptance
checks and specialists, and advances the goal only with a complete stack receipt.
A separate whole-goal final pass is required before publication resumes. Original
starting commits remain historical records. Refreshes use the original runtime,
token/spend and agent-run limits; they never extend the accepted budget.

Pause/cancel preserves refresh reservations and candidates. Resume reconciles
existing results; it does not automatically repeat an uncertain paid review.
Use `goal retry-verification` for an interrupted check/review before any ref-update
intent, within the accepted review-round limit. A partial ref update must recover
the same operation and proof. Conflicts and rewritten target history hold the goal.
Updated screenshot evidence must
be shared again after the new final pass; prior links remain in refresh history.

Refreshed final revisions retain the existing PR number and original ownership
marker. Publication follows the previous journal, proves the previous head is an
ancestor of the new verified head, and permits only a non-forced update from that
recorded head. A foreign remote revision, deleted acknowledged branch/PR, or
closed earlier PR holds publication. Members whose verified file tree already
matches the refreshed target need no new PR, including a human squash merge with
different commit ancestry. Human draft/ready state and PR descriptions are preserved.

New final evidence is appended as a separately marked comment through the
[GitHub issue-comment API](https://docs.github.com/en/rest/issues/comments#create-an-issue-comment),
which also handles PR discussion comments. Lost responses reconcile the same
comment across paginated reads; edited or deleted acknowledged evidence is never
overwritten or recreated. The comment includes the final repository combination,
captured target commits, checks, specialists, and newly shared artifact link.

If upstream moves again before integration starts, Lola archives the previous
target combination, all of its verification-round identities, and its supersession
time. It stops any journaled check containers before reserving a new capture.
Each capture consumes one of `target_refreshes`, even if superseded before fetching;
checks and paid reviews also remain subject to their original global budgets.
The controller checks targets before work and again before the first ref update.
The new target must descend from every superseded target, including captures
which were never fetched. Exhausting the capture limit holds the goal with its
evidence intact. Resume can follow a newer target within the remaining budget;
once any manifest or member ref-update intent exists, only recovery of that
original operation is allowed.

The publisher records its complete intent before writing upstream, then checks
GitHub's authoritative branch and PR state after each write. A lost response or
restart reconciles the same branch and owned PR. Partial stack publication
retains all acknowledged PR links. Deleted acknowledged branches, missing PRs,
closed PRs, and human edits are preserved instead of replaced automatically.
Draft creation uses the documented
[GitHub pull request API](https://docs.github.com/en/rest/pulls/pulls#create-a-pull-request)
with structured JSON input through
[`gh api`](https://cli.github.com/manual/gh_api).

`goal show` exposes each repository's PR URL and delivery state. Published drafts
leave the goal `ready`. Only an observed merge of the exact verified head into
every configured delivery target changes the goal to `accepted`. An unchanged
repository requires no PR. A goal with no changed repositories stays `ready`.
Lola's ordinary merge queue also excludes all `goal/...` branches.

Generated descriptions contain the goal and tasks, declared verification
results, risk notes, manual checks and the final repository combination. Raw
check commands and logs stay local because they may contain private configuration.
Descriptions are reserved with the publication intent and never overwrite human
PR edits. For a stack, reviewers must confirm the proposed repository order and
compatibility before merging.

If the accepted checks capture screenshots, share them as an artifact and attach
its HTTPS URL before publication:

```sh
lola goal delivery-evidence GOAL_ID --revision CURRENT_REVISION \
  --url https://YOUR_ARTIFACT_HOST/YOUR_SHARED_ARTIFACT
```

Lola links that artifact in the PR. It does not upload screenshots through
GitHub's undocumented browser endpoints. Ensure reviewers can access the shared
artifact; Lola does not fetch arbitrary supplied URLs or assume they are public.
The local screenshots and their hashes remain part of the verification evidence.
After the publication description is reserved, additional evidence belongs in
the existing PR, preserving the original journal.

Pause cancels a publishing command and holds subsequent writes. An interrupted
push or create may already have reached GitHub; resuming reconciles its durable
intent rather than assuming either success or failure. Cancellation retains
local evidence and remote work. Publication errors are separate from Linear
synchronization and do not erase the verified artifact.

## Goal usage and limits

```sh
lola goal usage <goal-id>
```

The response includes accepted limits, observed tokens and estimated dollars,
per-run daily totals, the latest observation time, and coverage counts. Every
reserved model call belongs to the denominator: absent telemetry appears as
missing, never as a known zero.

Coordinator and specialist calls collect numeric usage from the agent CLI's JSON
events, separately from the model's answer. Usage is persisted before a proposal
or verdict can be accepted, including when the command or result validation fails.
An interrupted review's numeric receipt can be recovered without another model
call. Failed and unaccepted proposals count toward project/global usage; accepting
a proposal reuses its ledger identity and cannot charge it twice.

The container image must support the provider's JSON event format:
[Claude stream-json](https://code.claude.com/docs/en/headless),
[Codex JSONL](https://learn.chatgpt.com/docs/non-interactive-mode), or
[OpenCode JSON](https://opencode.ai/docs/cli/). Missing or unsupported completion
events hold the result. Claude and OpenCode reported dollar costs are estimates;
Codex contributes tokens without an invented dollar price. Headless usage is
observed when the command returns and assigned to that local calendar day. It
cannot stop that same call mid-stream on a newly reported token limit; the finite
call/runtime limits still apply. A daemon failure before a receipt is written may
leave consumption unknown.

The usage loop scans each worker attempt's private agent logs, including repair
attempts, about once a minute. Claude transcript records and Codex cumulative
counters are read from bounded copies. OpenCode's database and WAL are copied
before querying with SQLite's documented
[safe, read-only CLI modes](https://www.sqlite.org/cli.html#the_safe_command_line_option)
and [untrusted-schema setting](https://www.sqlite.org/pragma.html#pragma_trusted_schema).
An unavailable SQLite CLI or an unsupported format yields missing telemetry.
Symlinks, special files, oversized snapshots and invalid counters cannot become
usage observations. Each snapshot is limited to 64 MiB and 4,096 entries; scans
have a shared time budget and prioritize older observations so they cannot
indefinitely delay the ordinary usage loop.

All transcript-derived totals are partial observations. Agents can modify their
logs; records may arrive late or be absent. Claude prices and OpenCode-reported
costs are estimates; Codex dollar cost is unavailable. These are not exact billing
limits. The finite call, attempt, review and runtime limits continue to bound work
when token telemetry is incomplete.

Each run/day keeps its highest observed counters. Rescans replace totals rather
than adding them, and rotation, missing files or daemon restart cannot erase
already recorded usage. Goal limits use raw reported tokens, including cache
traffic; existing project/global token budgets retain Lola's weighted-token
calculation. Private runs have distinct ledger sources and are excluded from
ordinary host-log attribution to prevent double counting.

Reaching an observed goal token or spend limit persists a blocked state and a
worker-stop request before canceling active verification. The controller then
stops workers while retaining their files and evidence. New calls and resume are
refused while the accepted budget is exhausted. A verified deliverable remains
available for human delivery. Stopping is subject to the scan/controller interval
and any missing telemetry; it is not a guarantee against billing overshoot.

The same command also returns `validation`: check attempts attributed to their
reserved integration, final-verification or refresh operation, including archived
retry and superseded-target rounds. It reads existing local receipts without
running or stopping containers. Repeated requests cannot charge a check twice.

The report shows completed wall-clock milliseconds (including container launch
and cleanup), accepted command timeouts, and the runner's resource ceilings:
2 CPUs, 2 GiB memory, 512 MiB scratch space and 256 processes. Those ceilings are
not measured CPU/memory consumption. Dollar cost is unavailable. Interrupted or
still-running durations stay null; a daemon outage is never counted as measured
check runtime. A missing receipt means not started **or** missing, not known zero.

Receipt inspection has a two-second cooperative deadline, a 64 MiB read budget,
and a 4 MiB per-receipt limit. `expected_checks`, `inspected_checks` and `partial`
show incomplete coverage; invalid or foreign receipts are counted separately.
This report is diagnostic and does not change verification or acceptance gates.

## Desktop and TUI goal controls

Open a project's detail view in the desktop app and expand **Goals**. Choose a
local goal to inspect its brief, acceptance criteria, tasks, attempts, usage
coverage, integrated commits and final PR links. The selected goal is remembered
per project for the life of that view. The section refreshes while open; a failed
read leaves its last snapshot visible with an error and disables actions.

**Pause**, **Resume**, **Stop workers** and **Cancel goal** first show their
consequences and the reviewed revision, with an optional reason. Opening an
inspection view never authorizes execution. Pause holds dispatch and integration;
stopping workers is separate. Cancellation keeps files, issues and audit history.
A changed revision or uncertain result is shown as an error and is never retried
automatically. Close the action and refresh before reviewing it again.

In the TUI, open project details and press **g** for Goals. Select a goal with
arrow keys and press Enter to inspect it. Arrow keys then scroll, Tab selects a
task, **s** opens its latest attempt's session, and **o** opens the goal in Linear.
The available control shortcuts are shown below the view: **p** pauses, **r**
resumes, **x** stops workers and **c** cancels. Enter applies the reviewed action;
Escape closes it. **f** refreshes. Long criteria wrap within the terminal width.

Both clients read the same cached overview and submit the same revision-checked
commands. Creation, planning, authorization policies, verification/replan retries,
shared delivery evidence, knowledge curation and profile configuration remain
available through the CLI; their app forms are still being implemented.

## Remaining work

The full implementation plan remains active: broader task/policy revision
reconciliation and steering, authoring/knowledge/profile forms and remaining UI
parity, and
improvement metrics. Measured container CPU/memory and dollar costs remain
unavailable; the usage report distinguishes them from resource ceilings. See
[the plan](../PLAN-self-improving-workspace.md).

## Finding decisions and calibration

`lola profile findings` lists stable finding IDs, the original input-bound review,
current state/revision and decision history. A retry preserves earlier findings.
Use the exact revision you inspected:

```sh
lola profile decide FINDING_ID --revision 1 --state accepted --evidence 'Reproduced the concurrent save failure in the regression test'
lola profile decide FINDING_ID --revision 2 --state fixed --fix-head FULL_COMMIT_SHA --evidence 'The fix serializes the complete read-modify-write operation'
lola profile decide FINDING_ID --revision 3 --state verified --fix-head FULL_COMMIT_SHA --verification CLEAN_REVIEW_KEY --evidence 'Independent review and regression evidence confirm the fix'
lola profile calibration
```

A reported finding can be accepted, disputed or dismissed. Accepted findings
can become fixed or disputed; a disputed finding needs an explicit accepted or
dismissed decision. A fixed finding can become verified or disputed. Verified
and dismissed claims can be disputed again if new evidence contradicts them.
Every decision requires bounded evidence. Identical retries retain one event;
a different decision against an old revision is rejected.

`fixed` is an operator claim naming an exact new commit. `verified` additionally
requires an existing clean, complete review at that commit, with the same
repository, profile, provider, rubric version and acceptance contract. Its receipt
is retained even if a later review is invalidated. These annotations never change
the original verdict or bypass required review/integration gates. A blocking
dispute remains a human decision; dismissal alone does not grant a merge.

Calibration reports state counts and unresolved blocking disputes, separated by
profile/provider/rubric/repository. Unassessed claims stay `reported`. These are
operator assessments, not measured reviewer accuracy or a claim of causal
improvement. Model/prompt-level comparisons, worker-submitted disputes and a live
pilot remain unfinished.

## Quick look at this checkpoint

The project/stack **Execution** settings and goal inspection/steering views are
available. Goal creation and execution authorization are currently CLI-first.
The checked-in plan lists the remaining work; this is a development checkpoint.

Build the branch binary with `make build`. Run `./lola goal --help`, `./lola learn
--help`, and `./lola profile --help` to inspect commands without starting work.
The desktop frontend can be launched from source with:

```sh
LOLA_BIN="$PWD/lola" make desktop-dev
```

This selects the branch binary for daemon starts; it does not replace an already
running daemon. An existing daemon must be deliberately restarted on the branch
binary before the new goal commands work. The installed app and daemon are not
updated by `make build`. The desktop source launch also requires the existing
Wails/frontend development toolchain documented in `desktop/README.md`.

For a separate first-run UI look, set `LOLA_HOME` to a new temporary directory
when launching the desktop. This gives it a separate configuration, state and
socket, with no configured polls or goals. It does not copy your projects or
produce populated goal data, and Lola still uses its shared tmux server. Do not
use a second home to manage the same live sessions concurrently.

No commands above publish Linear issues, authorize a paid planning/worker run,
or merge code by themselves. Ordinary configured polling still follows its
existing settings when using your regular Lola home. For a populated test, use
a deliberately disabled test project and the creation/publication steps in this
guide; publication writes to the selected existing Linear project.
