# Workspace goals, learning, and specialist reviews

Implementation is in progress. The commands below are available on the feature
branch. Bounded execution and inspection controls are implemented; the full
plan and live evaluation are still in progress. Publishing
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

### Plan, publish and authorize in the desktop

Open a project's **Goals** section and select **Plan a goal**. Enter a title,
desired outcome, acceptance criteria (one per line) and optional non-goals.
Planning limits, a model override and an optional new milestone name are under
**Planning limits and Linear milestone**. **Request task proposal** makes one
bounded paid call. It does not publish issues or start coding workers.

Review the proposed tasks, repository ownership, dependency order, criterion
coverage and open questions. **Sources and planning record** exposes the retained
committed files and full evidence record. Acknowledge the review and choose
**Accept as local goal**. Acceptance rechecks source commits and routing against
the exact proposal revision. Saved proposals remain available in the picker;
**Refresh result** only reads a retained result and never repeats a paid call.

For a local draft, **Publish to Linear** first displays the consequences, task
count, optional milestone and reviewed revision. Confirm to create or reconcile
the parent issue, tasks and dependencies in the existing Linear project. A stale
revision is rejected before local or remote changes. After an uncertain result,
close the action and refresh before explicitly reviewing another attempt.

For a fully published goal, **Set execution limits** loads the current goal brief,
configured repositories and applicable specialists. Supply at least one acceptance
check, its installed SHA-256-pinned container image, command, timeout and optional
evidence files. Evidence paths include the repository ID from the combined root.
Required specialists cannot be deselected; additional specialists can be selected.
Choose finite task, attempt, review, call, runtime and observed-usage limits.
Draft PR publication, task replanning and target refreshes start disabled.

**Review authorization** captures the complete proposed execution policy. Review
it and acknowledge the paid calls before **Authorize and start**. Execution may
begin once the daemon durably accepts that request. The daemon rechecks the exact
goal revision, routing, required profiles and current Linear bindings. A stale
or uncertain response cannot silently resubmit against newer work; reload current
settings first. Token/dollar telemetry may be delayed or missing, so those limits
are not exact billing caps. Merge decisions remain human-only.

Task-graph authoring and recovery are available in desktop, TUI and CLI.
Existing-issue adoption remains available through the CLI below. The TUI supports
the same planning, publication and explicit execution authorization steps described next.

### Revise a proposed task graph

Before accepting a coordinator proposal as a local goal, use
`lola goal edit-plan edit.json` to revise its task graph without paying for another
model call. First inspect `lola goal proposal PROPOSAL_ID`. The edit file contains
that proposal's `id`, `revision`, `project` (from `input.request.project`), a fresh
UUID `edit_id`, a reason of at least eight characters, and the full revised
`output` object. Keep the edit file unchanged when recovering an uncertain reply.

```json
{
  "id": "PROPOSAL_UUID",
  "project": "nori",
  "revision": 2,
  "edit_id": "NEW_EDIT_UUID",
  "reason": "Separate the API change from its acceptance regression",
  "output": {
    "tasks": [
      {
        "title": "Implement invitations",
        "description": "Reuse the existing invitation flow",
        "acceptance": ["Recipient acceptance integration test passes"],
        "apps": ["nori-api"],
        "label_ids": ["EXISTING_LABEL_UUID"],
        "paths": {"nori-api": ["src/invitations", "tests/invitations"]},
        "covers": [1],
        "blocked_by": []
      }
    ],
    "unknowns": []
  }
}
```

Replace the example identities, paths and criteria with those in the reviewed
proposal. Add, remove, split or reorder tasks by replacing the full task array.
`blocked_by` uses earlier 1-based task numbers, and `covers` indexes the original
user acceptance criteria; every criterion must still be covered. Each task
requires explicit repository/path ownership and a writable deliverable. Existing
label routing must select exactly its declared repositories. The daemon checks
current Linear project/label availability with reads only. Invalid graphs and
stale revisions are rejected without changing the saved proposal.

The returned record has a new revision. Review it again before `accept-plan`.
The original brief, snapshots, model identity, original `model_output` and numeric
usage remain retained. `edits` records each operator request, reason, resulting
revision and timestamp. Replaying an identical `edit_id` returns the current
record, including subsequent edits or acceptance, without rewinding history;
reusing it for changed content is rejected. Up to 50 edits are retained per
proposal. Accepted goals and execution repair proposals cannot be changed through
this command, including after an interrupted acceptance acknowledgment. This
command does not create Linear issues, start workers or change execution policy.
In the desktop, open **Plan a goal**, select a saved proposal, and choose
**Edit task proposal**. Expand each task to change its title, description,
acceptance criteria, goal coverage, dependencies, repositories, writable paths
and Linear labels. **Add task**, **Remove task**, **Move earlier** and **Move
later** update the graph. Moves preserve dependency references; removing a task
is blocked while another task depends on it. Remove or replace those dependency
references explicitly first. Every goal criterion must remain covered, and each
task needs a writable deliverable. Repository selections and labels must agree
with the stack routing shown in the form.

Add a reason and choose **Review revision** to inspect the complete replacement
and compare it with the saved proposal. Acknowledge the review before **Save
proposal revision**. The edit ID and exact source revision are retained before
the save. Navigation within the planner is locked while editing; cancellation
asks before discarding unsaved work. After an uncertain save, **Refresh saved
proposal** reads the current record without resending the change. If no edit was
saved, start again from that current revision. Saving clears any previous goal
acceptance acknowledgment. Review and accept the new revision separately.

**Sources and planning record** includes the revision history and original model
proposal. In the TUI, inspect a saved proposal and press **e** to edit its tasks. Use
**Up/Down** to select a task, **Enter** to edit its fields, **n** to add a task,
**x** to request removal, and **u/d** to move it earlier/later. Removal requires
confirmation and refuses referenced prerequisites. Moves remap dependency
numbers and refuse an order that puts a prerequisite after its dependent.

Task forms use **Up/Down/Tab** between fields, **Space/Enter** for coverage,
dependency, repository and label toggles, and **Enter** to edit multiline fields.
The focused field's help identifies its goal criterion, earlier task or routing
label. Empty paths on an included repository mean read-only context. Choose
**Keep task changes** to return to the draft graph. **m** opens questions and the
revision reason; choose **Keep notes** after editing. These actions change only
the in-memory draft. **Escape** and **Ctrl+C** protect unsaved edits with a discard
confirmation; Escape inside a line editor first returns to the form.

Press **r** to validate and review the complete revision. **Space** acknowledges
that review, then **s** saves the exact revision and edit ID shown. **v** shows
the exact edit request. A pending save blocks repeat submission; an uncertain
result keeps the reviewed content and requires **f** to read the saved proposal.
After saving or recovery, accepting the current proposal requires a fresh review
and acknowledgment. The planning record includes revision history; **v** on the
saved proposal exposes the original model output and complete provenance.

Proposal storage remains format 1 until the first operator edit, which atomically
writes format 2 with the original output and edit history. This version reads
both formats; opening an unedited format-1 store does not rewrite it. Older
binaries reject format 2 instead of silently discarding its audit history. For a
binary rollback, keep the format-2 `state/goal-plans.json` intact and restore a
compatible binary to resume proposal features. Do not lower the version number or
restore an older snapshot over newer decisions. Existing goal records and their
execution history use their existing storage independently.

### Plan, publish and authorize in the TUI

Open project details, press **g** for Goals, then **n** to write a goal brief.
Arrow keys or Tab move between fields. Enter opens multiline fields; Escape
returns to field navigation. Specify the outcome, observable acceptance criteria
and exclusions, plus finite planning limits. You can also select a new milestone
name or an existing milestone UUID and supply committed files as `member:path`,
one per line. **Request proposal** shows the task/time limits before making one
paid model call. Leaving an unsent edited brief prompts before discarding it.

Use **b** from Goals to inspect saved proposals, including requests left running
or with uncertain responses. **f** refreshes a retained result without requesting
another model call. Scroll through the task scope, dependencies, coverage, open
questions and committed sources; **v** toggles the full retained record. Space
acknowledges the review, and **a** accepts that exact revision as a local goal.
An uncertain acceptance disables another attempt until you refresh and review
again. **g** then opens the local goal.

For a draft or partially published goal, **u** opens a separate publication
review with the goal revision, task count and optional milestone. Enter creates
or reconciles its Linear resources; Escape closes the action. An uncertain
publication requires closing and refreshing before another reviewed attempt.
Neither proposal acceptance nor publication grants execution permission.

For a published, synchronized goal, **a** opens **Set execution limits**. The
initial request only reads current settings. Edit repository path prefixes, add
acceptance checks with commands, installed image digests and timeouts, and add
captured evidence when needed. Enter cycles a check's repository; blank means the
combined repository root. Evidence paths include the repository ID. Required
specialists stay selected; optional specialists can be toggled. Set finite task,
call, runtime and observed-usage limits, plus any draft-PR/replanning permissions.

**Review authorization** validates the policy and freezes it for inspection.
Scroll through the goal criteria, repositories, checks, evidence, specialists and
budgets; **v** shows the full policy. Space acknowledges paid agent calls, and
**a** authorizes and starts that exact reviewed revision. Escape returns to the
editable limits and clears acknowledgment. Leaving edited limits prompts before
discarding them. While submission is pending, repeated keys cannot resubmit or
leave the screen. If the response is uncertain, Escape closes and refreshes
Goals before another action; the daemon may already have accepted execution.
Token and spend limits use delayed observations, not exact billing caps. Final
merges remain manual.

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

In the desktop app, open a project and expand **Workspace learning**. Search or
filter lessons, select one to inspect its scope, source, evidence and full
provenance, or choose **Record a lesson**. Discoveries remain candidates by
default. The explicit-correction checkbox is a separate operator decision;
mandatory policy proposals remain proposals even when entered as corrections.
Optional path prefixes, goal scope, tags and prevention proposals travel with the
lesson.

**Review acceptance** shows the exact lesson version and asks for verification
evidence. You can watch committed `member:path` references, retain or deliberately
change an existing expiry, and explicitly select conflicting lessons to replace.
**Dispute lesson** records evidence and excludes that lesson from future guidance.
Each mutation has a separate review and acknowledgment step. A stale version or
scope is rejected. A pending request cannot be submitted twice; an uncertain
reply requires **Close and refresh lessons** before another decision.

New desktop captures allocate an identity before submission. The daemon retains
it with the lesson: replaying identical content returns its current curated
version, while reusing the identity for changed content, scope or correction
authority is rejected. Older CLI capture requests without an identity remain
supported. Navigating to another project discards replies from the prior view.

**Preview LEARNINGS.md export** rechecks accepted project lessons and returns
reviewable file contents. It writes no repository files. Copy the reviewed
content into a branch and use normal code review.

In the TUI, open project details and press **K** for **Workspace learning**.
Arrow keys select a lesson; Enter inspects it, **v** shows full provenance, and
**/** searches topic, summary, status, tags and paths. **n** records a lesson.
From an inspected lesson, **a** opens acceptance and **d** opens a dispute.
Tab or arrow keys select fields; Enter edits multiline text, toggles a choice or
runs the selected action. Bracketed paste works in text fields. Escape finishes
multiline editing and protects unsaved decisions with a discard prompt.

Choose **Review decision**, inspect the exact scope/version and evidence, press
Space to acknowledge, then **a** to confirm. Existing expiry is retained in
RFC3339 form, with a timezone; clear it explicitly to remove it. Conflicting
lessons require explicit replacement choices. Pending operations suppress repeat
keys; uncertain replies offer Escape to close and refresh, without resubmitting.
**e** previews the export in the terminal, with no repository writes. The desktop
and TUI use the same daemon commands and curation guards. Desktop improvement
controls and their TUI equivalents are described below.

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
lola learn accept LESSON_ID --version 1 --evidence "Reproduced in test XYZ" \
  --watch lola:internal/daemon
lola learn maintain --project lola
lola learn dispute LESSON_ID --version 2 --evidence "This no longer applies after migration XYZ"
lola learn export --project lola --output ./learning-proposal
```

Explicit corrections activate when they are scoped and do not conflict with
existing guidance. Technical discoveries need acceptance evidence. Resolving a
conflict requires naming the replaced IDs with `--supersedes`. Policy changes
remain proposals. Environment-specific lessons can receive an RFC3339
`--expires` timestamp on acceptance.

Acceptance can attach repeatable `--watch member:path` references. The member is
its configured project ID (a member of the owning stack for stack lessons); the
path is a committed file or directory, with `.` covering the whole repository.
Lola records the current local default branch, commit and path fingerprint. This
records freshness of operator-reviewed evidence, not proof that the lesson is
correct. It does not fetch remotes or inspect uncommitted working-tree edits.

Before a watched lesson enters a briefing, coordinator prompt, specialist review,
lookup or export, Lola checks those references again. Unrelated commits preserve
file-scoped guidance. Changed/deleted code, or a changed configured checkout or
branch, returns the lesson to candidate status while preserving its previous
verification and every delivered version. Reacceptance requires fresh operator
evidence and reacquires existing references even if `--watch` is omitted.

The existing five-minute maintenance pass also checks references and returns
expired lessons to candidates. `learn maintain` requests the same check for one
project and reports `current`, `changed`, `expired`, `unavailable`, `untracked`, or
`inactive`. Each batch has a five-second Git-read deadline. An unreadable branch,
missing repository, or timeout omits affected lessons for that request without
persisting a false change; a later successful check can make them available again.
Lessons accepted without references (including existing human corrections) remain
usable and are reported as `untracked`. Attach references explicitly when their
validity depends on code. Verified specialist lessons can acquire references
automatically through the promotion path below; mandatory rules remain proposals.

### Promotion from verified review findings

Marking a specialist finding `verified` requests a bounded promotion check.
The five-minute maintenance pass and `learn maintain --project ...` retry it
after target code or goal delivery becomes available. Promotion requires the
original uncurated review candidate, a human-verified finding, its independent
clean fix review with recorded invocation provenance, and matching committed
code on the configured local default branch. A worker's self-report, an accepted
finding without fix verification, or a clean review without that finding history
does not suffice.

Lola compares the reviewed paths at the exact fix commit with the configured
target's paths, using metadata only. It records automatic code references and
scopes the derived guidance to those paths (with member prefixes for stacks).
If no path manifest exists, the complete repository tree must match and remains
watched. More than 20 paths, missing/deleted code, unavailable commits and ambiguous
repository routing retain the candidate for human curation. No fetch, checkout,
merge or agent execution occurs during promotion. Matching code establishes
applicability of the recorded evidence; it does not prove a universal rule.

Goal findings remain scoped candidates until Lola has observed complete goal
delivery as `accepted`, and the configured local target contains the matching
code. A ready goal or published draft PR cannot produce project guidance. The
derived project lesson preserves a `review_proof` linking the original candidate
version, finding revision, verification key and fix commit. The original goal
candidate and its history remain unchanged. `learn maintain` reports `promoted`
with the new `lesson_id`, or `waiting` with the missing evidence.

Before retrieval or export, Lola rechecks both the code references and the review
proof. A disputed finding, invalidated verification or changed source candidate
returns the derived lesson to candidate status. Unavailable proof omits it for
that request. Retries never reactivate a lesson that was disputed or demoted.
An explicit `learn accept` can replace automatic verification with fresh human
evidence; `review_proof.replaced_by_human` retains the historical link, and code
freshness checks continue. Conflicts and mandatory-policy proposals always require
human handling.

Knowledge snapshots now write format version 2 while retaining version 1 read
support. Existing lessons, delivered versions and improvement decisions are
preserved. Older binaries refuse version 2 instead of discarding proof metadata;
restore a compatible binary to resume knowledge features after rollback. Do not
lower the file version or overwrite newer knowledge with an older snapshot.

Retrieval excludes candidates, disputes, superseded entries, expired lessons,
and unrelated projects/paths/goals. Launch briefings contain at most 8 KiB of
selected lessons and record their exact versions. Path-specific lessons are
available through explicit lookup and specialist review retrieval when affected
paths are known. Export creates a new directory containing a `LEARNINGS.md`
index and focused lesson files for ordinary code review; it does not edit an
existing repository documentation file.

## Improvement proposals

Lola derives improvement proposals automatically from saved lesson records. Two
reporting sessions with the same topic, summary and path scope produce a proposed
regression-test task. Whitespace differences are normalized; paraphrases and
contradictory summaries are not merged. Worker/reviewer reports from the same
session count once, and repeated operator/correction captures count as one source.
These are occurrence counts, not independent verification or an accuracy score.
Project-wide and individual goal scopes remain separate.

A worker or operator can suggest a concrete prevention while capturing a lesson:

```json
"prevention": {
  "kind": "regression_test",
  "change": "Add a disk-full regression test proving failed persistence does not release a dispatch reservation.",
  "validation": "Show the test failing on the old behavior and passing after the fix, then run required checks."
}
```

Put this optional object alongside `topic`, `summary`, and `evidence` in an entry
of `.lola/learnings.json` (or `learnings.json` for a goal worker). `kind` can also
be `lint` or `skill`. The change and validation descriptions are bounded, untrusted
suggestions. An explicit idea can appear after one report; `policy: true` entries
appear as policy proposals and never become active rules. Differing proposed
approaches remain visible for review. Expired, disputed, superseded, and
maintenance-invalidated source lessons are excluded.

```sh
lola learn improvements --project lola
lola learn improvement PROPOSAL_ID --project lola
lola learn improvement-history PROPOSAL_ID --project lola
lola learn lesson LESSON_ID --project lola --version 1
lola learn export-improvement PROPOSAL_ID --project lola \
  --revision SOURCE_REVISION --output ./improvement-proposal
lola learn decide-improvement PROPOSAL_ID --project lola \
  --revision SOURCE_REVISION --decision tracked --evidence "Tracked in ENG-42"
```

Use `--goal GOAL_ID` on these commands for a proposal belonging to one experiment.
Listings return up to 50 proposals and `next_after` for the next `--after` page.
Previews show at most 20 source references and five suggested approaches, with
explicit omitted counts. Inspect exact source versions using `learn lesson` to
see their evidence, including historical versions after curation.

Export creates `IMPROVEMENT.md` and `proposal.json` in a new directory. The brief
names the reported problem, scope, source versions, proposed changes, and required
verification. Assign it through the ordinary issue/branch workflow or an explicitly
authorized goal; reproduce the problem, run required checks, and review the change
before merging. Export does not create a Linear issue, start a worker, install a
skill/linter, change AGENTS.md, or modify acceptance gates.

Triage supports `tracked`, `dismissed`, and `reopened`, with a required reason or
normal task reference. It changes only the proposal annotation. Export and triage
require the exact source revision reviewed; new reports or changed source versions
make old decisions stale while retaining their history. Retrying the same latest
decision is idempotent. New evidence makes the proposal pending again. Unchanged
proposals and their decisions survive daemon restarts through the canonical lesson
records and saved decision history. `improvement-history` retains the complete
source-version receipt for each decision, even after disputes or expiry remove
the current proposal from the listing.

## Specialist profiles

Desktop **Settings → Review → Specialist reviewers** now offers Backend, UI/UX,
Security, Integration and Performance presets. Add a specialist, choose an enabled
agent provider, and enable it before saving. Several specialists can share one
provider while keeping independent review results. New specialists start disabled.
The provider controls the model for ordinary sessions; an autonomous goal retains
the profiles and models in its accepted execution policy.

**On PR open** enables an ordinary-session specialist pass. **Required for
delivery** holds Lola's merge queue until current evidence passes. **Rubric and
scope** edits additional instructions, rubric version, project/stack IDs, path
prefixes, evidence kinds and bounded timeout/attempt limits. Required evidence
for UI/UX, Integration and Performance remains enforced. Settings Save validates
the complete configuration; removing an enabled specialist's provider is rejected
without writing a partial configuration. CLI/config access remains available.

In the TUI, press **S** from the cockpit, then Tab to **Review**. **Add
specialist** opens the specialty chooser. Arrow keys select fields; Enter or
Space changes toggles and enum choices. Enter opens project selection or the
line editor for rubric, paths and extra evidence; Escape returns to field
navigation. **Ctrl-s** validates and saves the whole settings draft. Removing
a specialist changes only the draft until Save; Escape offers to discard unsaved
changes. Enable the chosen agent under **Providers** before enabling its profile.
Existing legacy review settings must use the displayed **m** migration action
before editing. Profiles and providers remain distinct sections of the same tab.

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
`publish_pr`, optional `replan` and `require_human_acceptance`, a reviewed `rollout`
when publishing a stack, and finite `limits`:

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

### Reviewed compatibility and human merge order

New execution authorizations with multiple repository members and
`publish_pr: true` require a `policy.rollout` object. Review and include it in
`authorization.json` before `lola goal start`; the CLI submits it with the same
goal revision, checks and limits. For example, for member IDs `api` and `web`:

```json
{
  "compatibility": [
    "The new API must continue accepting requests from the previous web client.",
    "Keep the new web feature disabled until the API deployment is verified."
  ],
  "merge_order": ["api", "web"]
}
```

Requirements must describe the actual goal; Lola does not infer compatibility
from repository order. Provide 1–20 nonblank, single-line requirements, each at
most 500 bytes. Include every authorized member ID exactly once in the order,
including members that might remain unchanged. Unchanged members need no PR and
are skipped during human delivery. A single-repository published goal may also
carry a rollout contract. A goal with publication disabled does not need one.

The requirements and order are part of the immutable policy digest. Workers,
repair planning and specialist reviewers receive the accepted contract. Final
verification and publication retain that identity; changing or removing the
contract cannot reuse its evidence. Final PR descriptions show every requirement
and list repositories in the accepted order, with verified commits and targets.
Large descriptions abbreviate task/check summaries to preserve the complete
rollout and all repository links. Neither the order nor the prose schedules a
merge or authorizes deployment. The human must verify intermediate compatibility
and rollout conditions before merging. Checks and specialist findings establish
only their declared coverage, not automatic proof of arbitrary prose.

Desktop and TUI execution authorization include **Compatibility requirements**
(**Compatibility** in the TUI) and **Human merge order**. Enter one requirement
and one repository ID per line; every member must appear exactly once in the
order. Publication for a stack requires both fields. For one repository they
are optional, but a supplied contract must be complete. Turning publication
off excludes the contract from authorization while retaining the form draft.

Review freezes the requirements, order and goal revision together. An uncertain
start response retains that review and prevents repeated submission until the
goal is refreshed. Goal details retain the accepted contract and distinguish
older goals that have no recorded contract. A repository listing is never an
inferred merge recommendation. The TUI supports multiline paste into these
fields; review, busy and uncertain states do not accept draft changes.

Saving the first rollout-bearing execution upgrades `state/goals.json` to
version 3 atomically. Reading old version 1 or 2 stores does not rewrite them;
unrelated goals, usage, receipts and old policy digests are retained. A failed
save leaves both disk and in-memory state unchanged. Subsequent writes retain
version 3. Older binaries reject this format rather than silently dropping the
contract. Roll back by restoring a compatible binary while preserving the store;
do not remove rollout fields or lower the version number. Restoring an earlier
snapshot after external effects could lose ownership or duplicate work.

Current CLI, desktop and TUI clients use the `goalStartV2` socket command when
authorizing execution. An older daemon rejects it instead of silently ignoring
new rollout or human-acceptance fields. Update and restart the daemon if it
reports that command as unknown; clients never retry through the legacy command.
The current daemon still accepts older, unextended policies through `goalStart`,
subject to the new stack-publication requirement.

Previously authorized stacks without this field remain readable and executable
under their original policy. Their newly generated descriptions explicitly state that
no reviewed rollout contract was recorded and that listing order is not a merge
recommendation. An active policy cannot be silently amended; use a new reviewed
authorization for work requiring a new rollout contract.

Already reserved publications keep their original title and description, even
after a binary upgrade changes the template. Recovery still requires the exact
same policy, manifest, targets and operation identity; it never rewrites an
existing PR or verification comment just to apply new wording.

`retry-verification` is available on paused or blocked goals, within the accepted
review-round limit. It stops interrupted validation containers and reserves a
new operation identity without deleting failed evidence. An integration that
already recorded a ref-update intent must recover using that original operation;
it cannot be abandoned by retry. Pause cancels verification and prevents ref
updates. Stop-workers is separate and preserves submitted verification work.

Observed goal token/spend aggregation is wired into the usage loop and displayed
in the goal overview. Reaching an observed limit persists a hold and worker-stop
request and cancels active verification. Reports can be delayed or incomplete;
this is not an exact billing cap. Run counts and runtime/attempt deadlines also
bound work. See [Goal usage and limits](#goal-usage-and-limits) for attribution,
recovery and unavailable telemetry.

### Separate human acceptance after delivery

For a new goal, set `"require_human_acceptance": true` alongside
`"publish_pr": true` in the execution authorization's `policy`. The option is
part of the immutable authorization. Existing goals and goals that omit it keep
their prior behavior: observed complete final delivery marks the goal accepted.

With the option enabled, all final PRs must first be observed merged at the
verified heads and targets. Child tasks can then reflect delivered work, while
the goal remains `ready` and its Linear progress summary identifies the pending
human decision. Lola does not republish merged work while waiting.

Inspect the delivered app, goal contract and retained evidence, then record the
decision against the revision you reviewed:

```sh
lola goal show GOAL_ID
lola goal accept-delivery GOAL_ID --revision CURRENT_REVISION \
  --reason 'Verified invitation acceptance with a fresh recipient in the delivered app'
```

The daemon rechecks Linear bindings, execution policy, disputes and independent
final verification before accepting. An open or partial delivery, stale revision,
paused goal or invalidated proof is refused. The reason is required and retained
with the reviewed revision, final operation/receipt, policy digest, commit
manifest and decision time. It does not authorize merging or deployment.

An exact retry after a lost reply returns the original decision; changing its
reason or reviewed revision cannot rewrite that decision. `goal show` exposes
the receipt under `execution.human_acceptance`.

In the desktop execution authorization form, enable **Require a separate human
acceptance decision** and review that choice alongside PR publication. In the
TUI authorization form, enable **Require acceptance**; the help and final review
identify it as a separate human decision after final delivery. Both clients
reject this combination when PR publication is disabled.

Once delivery is observed, the goal view shows **Awaiting human acceptance**.
Choose **Accept delivered goal** in the desktop, or **h** in the TUI, then review
the criteria and delivered commits and enter acceptance evidence. Empty evidence
cannot be submitted. The TUI review supports arrow/Page keys and Home/End to
inspect long contracts; typing or pasting follows the evidence text. Confirming
submits the reviewed goal revision. An uncertain response freezes the action:
close it to refresh the goal before another decision. The saved acceptance shows
its original reviewed revision, time and evidence; accepted goals offer no
further execution controls.

The first saved authorization using this option atomically upgrades
`state/goals.json` from version 1 to version 2. Reading or writing goals without
the option keeps version 1 until that upgrade. Existing goals, usage, reservations
and journals are retained; failed writes do not publish the migration or decision.
After upgrading, all later writes retain version 2. Older binaries supporting
only version 1 refuse this store, so they cannot silently drop the acceptance
requirement. Roll back by restoring a compatible binary while keeping the current
store; do not lower its version or overwrite newer records with an old snapshot.

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
PR edits. For new published stacks, descriptions retain the reviewed compatibility
requirements and human merge order from the authorization. Legacy authorizations
without a rollout contract explicitly leave that decision unrecorded; configuration
order is not presented as a merge recommendation.

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
2 CPUs, 512 MiB scratch space and 256 processes. Check memory defaults to 2 GiB;
an explicit `memory_mib` in the reviewed check policy permits 256–8192 MiB.
It is available in the desktop and TUI authorization forms and bound into the
policy digest and check receipt. Existing policies retain their original digest
and 2 GiB limit. Each run reports its accepted memory ceiling; the report-level
ceiling is the maximum of the default and authorized check limits. Those ceilings are
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
arrow keys and press Enter to inspect it. Arrow keys and Page Up/Down scroll;
Home/End jump to the beginning/end. Tab selects a
task, **s** opens its latest attempt's session, and **o** opens the goal in Linear.
The available control shortcuts are shown below the view: **p** pauses, **r**
resumes, **x** stops workers and **c** cancels. **u** reviews publication for an
unauthorized draft. **a** sets execution limits for a published, synchronized
goal. Enter applies the reviewed control or publication action;
Escape closes it. **f** refreshes. Long criteria wrap within the terminal width.

Both clients read the same cached overview and submit the same revision-checked
commands. Both support coordinator proposals, local plan acceptance and Linear
publication and explicit execution authorization; the CLI supports these too.
Task-graph editing is available in both clients with a reviewed revision save.
Verification/replan retries and shared delivery evidence are available in both
clients and the CLI. Knowledge capture, provenance, curation and export preview are
available in both desktop and TUI.

The recovery API now provides a separate on-demand local snapshot via
`goalRecovery`: current brief, pending Linear source, latest accepted brief
comparison, latest progress update/error, shared evidence URL, and eligible
recovery choices. It performs no remote read or mutation and omits local
verification receipt paths and execution policy. Its choices are local hints;
actual actions still recheck jobs, budgets, Linear and publication journals.
In the desktop Goals panel, select **Recovery and Linear** to inspect this snapshot.
Ordinary goal controls and selection are held while it is open. Choose an available
action, inspect its consequences and provide any required reason, then review the
frozen revision before confirming. Brief reconciliation shows the exact Linear
source, retained task acceptance and current/proposed contracts. Dismissing a
progress update retains the update and reason without claiming it was delivered.
The shared HTTPS evidence URL is a reference; attaching it does not upload files
or establish verification. Verification and replanning retries may use paid calls
within the accepted budgets.

An unconfirmed response keeps the reviewed request visible and disables repeat
submission. **Reload local snapshot** reads the saved result before another
decision; a failed reload keeps the lock. Closing an editable draft offers an
explicit discard choice. The snapshot also shows the latest accepted brief
comparison, latest progress resolution and shared evidence reference.

In TUI goal details, **e** opens **Recovery and Linear**. Arrow keys and Page
Up/Down scroll the local snapshot; Tab or Left/Right selects an available action.
Enter opens its fields. Use **Ctrl+V** from the form to inspect source context,
then Escape to return. List fields support multiline paste and Enter for a new
line; Escape finishes field editing. Select **Review recovery decision**, inspect
the frozen request, then press **a** to confirm. Escape from review returns to
editing. After an unconfirmed reply, **f** reloads the local snapshot without
repeating the action. A failed reload keeps confirmation locked. Unsaved drafts
require explicit discard on exit. All six operations remain available in the CLI.

The improvement inspection API supports a paginated catalog that includes
withdrawn proposals with retained decisions. Source references are paged against
an exact current or decision-retained revision, so the preview's 20-source cap
does not hide evidence from inspection. Decision summaries omit the large source
receipts; those references remain available through the source pages and the
original CLI history command. Exact lesson versions are read separately within
the same project and goal scope.

In desktop **Workspace learning → Improvement proposals**, leave Goal scope
blank for project-wide proposals or enter an exact goal ID and load that scope.
Choose a proposal to inspect its current brief and retained decision history.
**Inspect all sources** pages through every exact lesson reference; open one to
read its recorded version, evidence and full provenance. Recorded status is
historical and does not establish current eligibility. A withdrawn proposal
retains history and its saved source references but cannot be triaged or exported.

Choose **Track**, **Dismiss** or **Reopen**, enter decision evidence, review the
frozen revision and confirm separately. After an unconfirmed response, use
**Reload saved proposal**; confirmation remains locked until readback succeeds.
**Preview improvement export** exposes copyable content without writing files or
installing rules. Implementation still follows an ordinary task branch and code
review.

In the TUI, open **Workspace learning** from project details with **K**, then
press **i** for improvements. **g** changes the exact goal scope; leave it blank
for project-wide proposals. Arrow keys select a proposal and Enter inspects it.
**n** loads the next catalog or source page. In proposal details, **s** opens
sources, **h** opens decision history and **e** previews the export. Enter on a
history decision inspects its retained source revision; Enter on a source opens
that recorded lesson version. **v** toggles full provenance. Page Up/Down and
Home/End scroll long evidence without changing selection.

Use **t** to track, **d** to dismiss or **r** to reopen. Enter edits multiline
evidence; Escape ends editing. Select **Review decision**, inspect the frozen
scope/revision/evidence, then **a** confirms. Escape from review returns to the
retained draft; leaving a changed form requires an explicit discard choice.
After an uncertain response, **f** reads the saved proposal and history without
repeating the write. A failed reload leaves confirmation locked. Withdrawn
proposals retain history inspection but expose no triage/export controls.

## Remaining work

The [completion audit](workspace-goals-audit.md#finish-checklist) lists the exact
remaining validation work: a bounded live pilot and representative outcome comparisons. Measured container
CPU/memory and dollar costs remain unavailable; the usage report distinguishes
them from resource ceilings. See [the plan](../PLAN-self-improving-workspace.md).

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
and dismissed claims can be disputed again by an operator if new evidence
contradicts them.
Every decision requires bounded evidence. Identical retries retain one event;
a different decision against an old revision is rejected.

`fixed` is an operator claim naming an exact new commit. `verified` additionally
requires an existing clean, complete review at that commit, with the same
repository, profile, provider, rubric version and acceptance contract. Its receipt
is retained even if a later review is invalidated. These annotations never change
the original verdict or bypass required review/integration gates. A blocking
dispute remains a human decision; dismissal alone does not grant a merge.

For reviews owned by the same goal and task, the appended repository combination
may change between the finding and its verified fix. Lola compares the unchanged
acceptance requirements separately from that valid commit manifest; both complete
contracts and combinations remain part of their respective review identities.
Different requirements, task/goal ownership, or malformed manifests cannot use
this comparison.

Calibration reports state counts and unresolved blocking disputes, separated by
profile/provider/rubric/repository. Unassessed claims stay `reported`. These are
recorded assessment states, not measured reviewer accuracy or a claim of causal
improvement. `worker_disputes` separately counts findings that received an
attributed worker dispute, including those later resolved by a human.
The reproducible [comparison record workflow](comparison-records.md) is implemented.
Representative live comparisons and a live pilot remain unfinished.

### Comparable-run reporting

Use `lola compare template` to create a plan, `lola compare prepare plan.json`
to prepare baseline/treatment run drafts, and `lola compare report records.json`
(or `--markdown`) to report filled observations. These commands are read-only
with respect to Lola and external services; retain the printed JSON records and
reports alongside their cited evidence. They do not start agents.

The versioned records bind task, starting repositories, criteria and limits;
retain model/prompt/rubric/lesson versions; and distinguish observed, reported,
estimated and unavailable measurements. Reports retain all planned pairs,
exclude unmatched or unverified-budget pairs from aggregates, show missing
metrics explicitly and include failed-run cost in cost per accepted task.
See [recording rules and commands](comparison-records.md). A passing reporting
test is not evidence of better live outcomes.

### Review provenance and compatibility

Every new review records `inputs.invocation`: the agent, requested model, execution
environment, exact instruction text and its SHA-256. The record already retains
the task contract, repository head/base, artifacts and supplied lesson text.
The provenance participates in the review key. Changing the model, rubric or
prompt requires fresh evidence; a previous result cannot silently satisfy the
new configuration's gates or deliver stale feedback. Both host and container
adapters check the reservation before launching. Goal recovery retains existing
budget reservations and requires explicit verification recovery when the old run
no longer matches.

`lola profile calibration` separates requested models, prompt hashes and execution
environments in addition to profile/provider/rubric/repository. `model_selection`
is `explicit`, `agent_default`, or `unknown`; `provenance` is `recorded` or
`legacy_unknown`. A requested model is **not** proof of the exact model served:
provider aliases and agent defaults can change, and resolved model identities are
currently unobserved. These counts alone cannot establish comparable-budget
accuracy or causal improvement. Inspect full review records for source evidence.

The specialist store upgrades version 1 to version 2 atomically on open/save,
preserving reviews, findings and decision history. Legacy records keep unknown
provenance and remain inspectable; they do not acquire an invented model/prompt
or satisfy a new review gate. Ordinary configured reviews can run afresh. Existing
goal evidence that lacks provenance holds for explicit verification recovery.

Older binaries that support only store version 1 refuse version 2 instead of
rewriting it and dropping provenance. Rolling back such a binary leaves specialist
review unavailable until a compatible binary is restored. Keep the version 2 file;
do not manually lower its version or restore a stale snapshot over newer findings.
Configuration and ordinary provider records use their existing formats.

### Worker disputes

Specialist findings sent to ordinary workers now include their immutable finding
IDs and current revisions. Delivery respects the provider's **Send to agent**
setting and the existing ownership, empty-composer, idle and sanitization gates.
Each specialist attempt has its own durable delivery guard, including specialists
sharing one provider. Stale-head feedback is discarded. Goal repair prompts carry
the same finding references. Large feedback previews are explicitly marked as
truncated while retaining every finding ID; full records remain in Lola.

A worker can submit a bounded JSON array in `.lola/review-disputes.json` inside its
repository, or `review-disputes.json` at the top level of a goal workspace:

```json
[
  {
    "id": "FINDING_ID",
    "revision": 1,
    "evidence": "The reported concurrent-write reproduction bypasses the public transaction wrapper; the concurrent API test demonstrates the counterexample."
  }
]
```

Use the exact revision received. At turn end Lola reads at most 20 entries and
64 KiB, rejects symlinks, special files and unknown fields, and establishes the
worker identity itself. Stack lanes use their own repository report files. The
goal controller also collects reports before advancing work, covering a missing
Stop callback. A goal worker must belong to a current reserved/running/submitted/
verifying attempt. Its finding must belong to the same goal and task, or a
recorded ancestor task that was decomposed into its replacement. Ordinary workers
can dispute only findings owned by their own session. Legacy records without
owner metadata require operator curation or a fresh review.

The event records worker/session/task attribution separately from human decisions.
Workers can only dispute: they cannot dismiss, verify, or reopen a human-resolved
finding. Each finding accepts at most one worker dispute; identical retries are
idempotent, including a retry after a human has resolved it. Further disagreement
requires human curation rather than another automatic debate.

An unresolved major/blocker dispute holds ordinary delivery gates. For a goal it
also records a visible blocked reason, cancels ongoing verification, and requests
worker shutdown while preserving their files. It prevents dispatch, repair,
integration, finalization, publication and resume. Resolve the finding with
`lola profile decide ... --state accepted|dismissed --evidence ...`, then explicitly
resume the goal. Resolution never changes the original review verdict: required
independent verification must still pass.

## Quick look at this checkpoint

The project/stack **Execution** settings and goal inspection/steering views are
available. Both desktop and TUI include goal planning and Linear publication.
Execution authorization forms are available in both clients. Task-graph editing
is available in desktop, TUI and CLI with a separate revision review before save.
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
