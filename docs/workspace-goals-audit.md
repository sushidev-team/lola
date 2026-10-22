# Workspace goals completion audit

Audited 2026-10-22 on `feat/self-improving-workspaces`, including the
implementation and pilot fixes in this checkpoint. Source and automated evidence are
supplemented by the bounded live Nori run described below. That run verified
publication recovery, worker launch, daemon restart and budget-stop preservation;
it did not reach independent verification or final delivery. The normal installed
application and daemon have not been replaced as part of this checkpoint.

The original requirements are in [the implementation plan](../PLAN-self-improving-workspace.md).
The plan's earlier 75% figure was a rough estimate. The statuses below replace
that estimate; counting files or passing tests would not produce an honest
completion percentage.

## Requirement coverage

**Implemented** means the path exists and has automated coverage. **Partial**
identifies a specific missing part. **Unverified** means a claim needs external
or empirical evidence, even where implementation exists.

| Plan requirement | Status | Evidence and remaining boundary |
| --- | --- | --- |
| §1–3 Reuse Lola's runtime, provider, status and budget layers | Implemented | Goal controller, isolated runtime and specialist adapters compose the existing packages. Goal sessions stay outside ordinary delivery and merge paths (`internal/daemon/goalrunner_test.go`). No second orchestration service is introduced. |
| §4 Existing Linear project, goal parent, optional milestone and task dependencies | Implemented | `internal/daemon/goals.go` and `goals_test.go`: reserve IDs before writes; reconcile partial publication and lost responses; preserve existing project and per-task labels. No initiative creation. |
| §4 Adopt existing work without replacing human hierarchy or milestones | Implemented | `goaladoption_test.go` exercises related-issue membership, preservation of the adopted issue, restart and ownership detection from another Lola home. |
| §4 Separate membership, routing and execution authority | Implemented | Routing includes unconditional stack members. Goal ownership excludes parent, tasks and unknown markers from ordinary polls, including a parent with zero open children. Authorizing a goal is a separate revision-checked action. |
| §4 Human edits, deleted bindings, progress and workflow synchronization | Implemented | Confirmed binding drift holds instead of recreating work. Owned progress and workflow writes retain ambiguous outcomes and preserve human changes (`goals_test.go`, `goalprogress_test.go`, `goalworkflow_test.go`). Desktop/TUI/CLI support brief synchronization, explicit source/revision-bound brief acceptance and uncertain-progress resolution, including local readback after lost replies. |
| §4 Optional separate human acceptance of the parent goal | Implemented | New authorizations can require a separate human decision after complete exact-head delivery. The receipt pins the reviewed revision, final proof, policy and manifest; bindings and independent evidence are rechecked. Exact lost-response retries preserve the decision. Goal store v2 prevents old binaries dropping the requirement. Desktop/TUI include the policy choice, reviewed evidence entry, uncertain-response recovery and saved decision. |
| §5 Scoped durable lessons, provenance, conflicts and export | Implemented | `internal/knowledge` retains versions, source evidence, conflicts and delivered references. Explicit corrections can activate; worker claims remain candidates. Selection is bounded and project/goal/path scoped. Desktop and TUI support capture, acceptance, dispute and export preview. |
| §5 Automatic verified learning and maintenance | Implemented for the defined proof path | Stop hooks capture worker reports; the maintenance pass promotes independently verified specialist fixes only after accepted code matches, and retracts on disputes or code drift. Other technical discoveries need explicit acceptance evidence. Automatic validation of arbitrary discoveries is not established and is not necessary to accept them safely through curation. |
| §5 Recurrence proposes tests, lint rules or skills | Implemented | `knowledgeimprovements_test.go` verifies distinct-session recurrence, source-bound triage and export. Proposals neither activate lessons nor install policy. Desktop and TUI inspection/triage/history/export are implemented. |
| §6 Specialist rubrics, conditional triggers and fresh review evidence | Implemented | Backend, UI/UX, security, integration and performance profiles; provider/model/rubric/input identity; UI screenshots and interaction evidence. Same-provider profiles are independent; unknown evidence cannot become a clean result (`reviewprofiles_test.go`). Settings exist in desktop and TUI. |
| §6 Finding decisions, disputes and safe handoff | Implemented | Reported, accepted, disputed, fixed and verified states retain evidence. Worker disputes are scoped and cannot impersonate human decisions. Blocking disputes hold progress. Finding decisions do not bypass delivery gates (`profilefindings_test.go`, `profiledisputes_test.go`). |
| §7 Explicit bounded contracts and repository-informed planning | Implemented | Per-project/stack opt-in; separate execution authorization; finite limits, checks, scope and profiles. Durable proposals and reviewed graph revisions exist in CLI, desktop and TUI. Failed or ambiguous paid calls are retained rather than automatically repeated. |
| §7 Bounded repair/decomposition; permission changes need human decision | Implemented within the accepted contract | Conclusive failure can trigger bounded repair or decomposition. Child work retains original coverage and narrows ownership. Brief clarification is explicitly accepted while paused. Arbitrary replacement of an executing task graph or raising its policy limits is refused; that is a safe boundary, not permission for a coordinator to expand scope. |
| §7 Durable tasks, attempts, verification and integration recovery | Implemented | Reservations precede effects. Runtime launch receipts and integration journals prevent duplicate work. `goalrunner_recovery_test.go` uses the real runtime with fake tmux and forces post-launch session/goal persistence failures; reopening the stores restores the original worker without a second launch or budget reservation. |
| §8 Private goal/attempt stores and human-only final merge | Implemented | Temporary Git tests prove original checkout/main stay unchanged and object stores are independent. Worker credential and mount tests cover launch boundaries. Publisher tests reject unsafe targets before remote writes; manually opened goal branches remain excluded from ordinary automatic merging. |
| §8 Stack manifest, combined verification and partial recovery | Implemented | Actual two-repository fixtures inject failure between ref updates, reopen the manager, recover the existing intent and retain the coherent manifest (`internal/goalwork/manifest_test.go`). Dependent attempts receive verified commits. |
| §8 Final linked PRs, compatibility constraints and human merge order | Implemented | New stack publication authorization requires explicit compatibility requirements and an exact member order. The immutable policy binds worker/reviewer context, verification and final PR descriptions. Version 3 retains the contract through restart and refuses older readers. Legacy authorizations retain their original digest and no longer present configuration order as a merge recommendation. Desktop/TUI authoring, frozen review, uncertain-response handling and accepted-contract display are implemented. |
| §9 Attributed usage, reservations, stop behavior and uncertainty | Implemented with telemetry limits | `goalusage.go` is called by the usage loop; reaching observed limits persists a hold and stop request. Tests cover cancellation, dirty-file preservation, restart and idempotent ledger totals. Worker logs can be delayed/partial; actual container CPU/memory and dollar cost are not measured. Finite call/runtime limits still apply. |
| §9 Linear/Lola visibility and steering | Implemented | Both clients show tasks, attempts, budgets, delivery and basic controls. Planning, graph editing, publication and authorization are available in both. Both clients now support verification/replanning retries, shared evidence and brief/progress recovery. Both clients support improvement triage and complete source/history inspection. |
| §10 Phase exits 0–4 | Automated evidence present | Failure and recovery tests support these boundaries with fakes, temporary Git repositories and fixture UI captures. The live pilot additionally proves Linear publication recovery, container worker launch, daemon restart and budget stopping. Specialist review, independent integration and final PR publication still lack live pilot evidence. |
| §10 Phase 5 and §12 measured improvement | Reporting implemented; outcomes unverified | `internal/comparison` and `lola compare template/prepare/report` provide versioned paired records, task/base/budget identity, provenance, explicit unavailable measurements and deterministic JSON/Markdown reports. No representative live comparison yet establishes fewer repeat errors, less intervention or acceptable cost per accepted task. |
| §13 Existing Nori project/labels, no initiatives, human final merge | Implemented design; pilot stopped at token limit | No additional initiative/project is required. The user selected dialog layout improvements matching the current style, capped at 30 minutes; Nori is the project assumption from the question and the initial chosen ceiling is 8 total agent calls. Repository/dialog scope, team-state mapping, offline dependencies and baseline acceptance artifacts are established in the pilot evidence. Container authentication and team PR automations have been verified. The first live run retained its work on a token-budget stop; final verification and delivery remain pending. |

## Required verification gates

These references identify assertions inspected during the audit, not just test
names discovered by search. The final `make check` after the live-discovered fixes
passed; prior frontend results still apply because those fixes changed only Go
code and documentation. Live observations are distinguished from test fixtures.

| Plan §11 gate | Evidence | Qualification |
| --- | --- | --- |
| 1. Partial provisioning, lost response and retry | `internal/daemon/goals_test.go`: `TestGoalPublishRetryAndRestart`, `TestGoalLostResponsesRecover` | Fake Linear commits a write and then loses the response; reopened stores reconcile without duplicate creations. |
| 2. Dispatch exclusion in all poll modes | Same file: `TestGoalDispatchExclusionAllPollModes` | Label, state, seen and label-free cases; ordinary issue still dispatches. |
| 3. Parent exclusion after children finish | Same test uses a parent with zero open children | Exclusion derives from ownership, not current child count. |
| 4. Per-task and always-included routing | Same file: `TestGoalStackRoutingIncludesUnconditionalMembers` | Missing mandatory members and mismatched labels are rejected. |
| 5. Human changes, canceled/deleted bindings and outages | `TestGoalConfirmedBindingDriftHolds`; `goalrunner_test.go`: `TestGoalScopeDriftHoldsAndResumeRequiresCurrentBindings`; `goalworkflow_test.go` | Mutation cases include edited text, moved project, cancellation, archival, labels, deleted issue/relation/milestone. An unavailable binding lookup blocks launch and resume. |
| 6. Crash after external success before recorded outcome | `internal/daemon/goalrunner_recovery_test.go`: `TestGoalLaunchRecoversAfterPostLaunchPersistenceFailure`; `internal/goalwork/workspace_test.go`: `TestIntegrationRecoversCASBeforeReceiptCommit`; `manifest_test.go`: partial updates | Forced session and goal snapshot failures after a successful real-runtime launch, followed by reopened stores: one launch invocation, original attempt/run, preserved worker output, dependencies still held. Integration tests cover the analogous ref-update/receipt window. |
| 7. Stale review inputs and unavailable verification | `reviewprofiles_test.go`; `goalverify_test.go`: `TestGoalVerificationFailuresNeverAdvance`; proposal/replan validation suites | Inspected review/check cases preserve goal refs when evidence is missing, unavailable, mutated or invalidated. Coordinator malformed-output coverage exists in the suites; it is not a live model-quality evaluation. |
| 8. Profile independence and compatibility | `reviewprofiles_test.go`: independent IDs and keys; profile invocation/legacy migration suites | Same-provider profiles cannot satisfy one another. Old provenance stays unknown and must not establish a fresh gate. |
| 9. Goal merge exclusion and forbidden writes | `goalrunner_test.go`: `TestGoalSessionsStayOutsideOrdinaryGitAndMergePaths`; `internal/goalpublish/publish_test.go`: `TestPublicationFailsClosedBeforeWrongOrUnverifiedWrites` | Even manual goal-branch sessions are excluded. Denied/default/foreign/changed/unknown publication inputs cause zero remote writes. |
| 10. Partial stack integration | `internal/goalwork/manifest_test.go`: `TestStackIntegrationJournalRecoversPartialUpdates`, `TestStackVerificationIncludesUnchangedMembers` | Real temporary Git repositories; failure on the second ref update; unchanged members also require proof. |
| 11. Budget/pause/cancel and preservation | `goalusage_test.go`: `TestGoalUsageStopsActiveWorkAndPreservesFiles`; `goalverify_test.go`; runtime stop tests | An observed exhausted budget blocks, cancels validation and stops workers while retaining a dirty file. Restart cannot erase consumption or allow resume. |
| 12. Learning evidence, conflicts, scope and bounds | `knowledgepromotion_test.go`; `knowledge_test.go`; `internal/knowledge/knowledge_test.go` | Unmerged fixes wait; accepted code promotes; code drift/dispute retracts. Conflicts require resolution; unrelated scope and oversized briefing are excluded; supplied versions survive restart. |

## Finish checklist

This is the remaining scope. General descriptions such as “richer controls” or
“better agents” are not additional completion criteria.

- [x] Add a regression for successful worker launch followed by failed session or goal persistence; prove recovery from reopened stores does not create another worker or release dependencies, and preserves worker output.
- [x] Add separate optional human acceptance with durable exact-revision evidence, daemon/CLI commands and desktop/TUI authorization and decision controls.
- [x] Finish desktop/TUI authoring and reviewed display of the explicit stack rollout contract. Domain, CLI, worker/reviewer context and final PRs now retain compatibility requirements and human merge order; final merges stay manual.
- [x] Complete desktop/TUI recovery parity: verification/replan retry, shared evidence URL, Linear brief review/acceptance and uncertain-progress resolution. Both clients use the on-demand local snapshot, frozen reviewed requests, separate confirmation and readback after uncertain outcomes.
- [x] Complete desktop/TUI improvement proposal inspection, source/history inspection and revision-bound triage/export. Both clients retain scoped exact-version evidence, frozen reviewed decisions, uncertain-response locks and readback, withdrawn history and read-only export.
- [x] Provide reproducible matched-budget comparison records/reports for repeat corrections, assessed findings, regressions caught, human intervention time and cost per accepted task; retain unavailable metrics explicitly. The portable JSON workflow and deterministic reports are implemented; empirical comparisons remain separate below.
- [ ] Complete the bounded live pilot through independent verification, specialist review and a final human-only draft PR. Publication, worker launch, restart and token-budget stopping have been observed; the stopped run is not a successful delivery.
- [ ] Run representative comparisons and report results and limitations. Do not claim improvement from finding counts alone.

Automatic verification of every kind of worker discovery, embeddings, arbitrary
policy increases during a run, a new orchestration framework, and a second
backlog are not requirements for finishing this plan.

## Verification and checkpoint limitations

Comparison reporting passes focused pure-domain, daemon-route and CLI tests,
plus a domain race check. Tests cover deterministic output and input preservation,
missing pairs, rejected/incomplete-run denominators, unavailable versus zero,
cost estimates, explicit provenance confounders, overrun/unknown usage exclusion,
strict schema/contracts and unfilled-draft rejection. The daemon route is tested
with no stores/runtime/integrations and a canceled context; the CLI sends one
request and preserves the input. Full `make check` passed (build, vet and tests;
`/tmp/lola-comparison-make-check.log`). These fixtures
are explicitly synthetic; no observed improvement is claimed.


TUI improvement parity passes focused behavior/capture tests and final full
`make check` (`/tmp/lola-tui-improvements-tests-final.log`,
`/tmp/lola-tui-improvements-make-check-final.log`). Coverage includes all three
triage payloads, immutable review data, confirmed and uncertain readback,
pagination, old decision sources, wrong versions/scopes, export errors, paste
and explicit draft discard. Terminal bounds cover 120×40, 80×24 and 40×12 with
long error text, persistent reload keys and scrollable long history. The fresh
reviewer inspected all twenty-four wide/compact captures and implementation and
returned `ship` with no material findings. This is offline evidence, not proof
of live integration or measured improvement.


Improvement inspection now has local read APIs for a paginated catalog (including
withdrawn proposals with decisions), complete exact-version source references,
and decision summaries. Historical references remain available only when retained
by a durable decision; an old unrecorded preview is not reconstructed from newer
lessons. The original full-receipt CLI history remains unchanged. Typed desktop
methods expose these reads, exact lesson versions, triage and revision-bound
export; they do not retry mutations or write checkout files. Desktop forms consume these methods; equivalent TUI controls use the same daemon commands. Focused store/daemon/bridge tests and the store race check pass; generated
bindings pass type checking with zero errors/warnings and the production build.
Full `make check` also passed after the refreshed frontend build
(`/tmp/lola-improvementviews-make-check.log`). This is the inspection/bridge
foundation; desktop and TUI improvement forms are now implemented.

Desktop improvement behavior tests pass (full frontend: 70 files / 825 tests),
as do zero-error/warning type checking and production build. Coverage includes
complete source pagination, exact historical lesson versions, withdrawn history,
frozen triage requests, lost responses and failed readback, revision-bound export,
UTF-8 evidence limits, scope changes and stale reply exclusion. The independent review requested two fixes: stale selector state after readback
and missing draft protection for reviewed unsaved evidence. Regression tests and
recaptures address both; the reviewer scored both resolved (`ship`). This verdict
pass covers those fixes, not a new full-surface review. Eighteen offline captures
are retained. Final `make check` passes against the refreshed frontend bundle
(`/tmp/lola-improvements-ui-make-check-final.log`). This is not live pilot evidence.


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

The recovery snapshot and desktop bridge foundation passed focused domain,
daemon and bridge tests, a domain race check, frontend type checking and
production build, and full `make check` (`/tmp/lola-recovery-make-check.log`).
Tests prove unchanged source state and paid-call reservations, retained exact
review identities, local-only reads, unavailable exhausted retries, omission of
private execution policy/receipt paths, and no automatic bridge retries. These
checks establish the foundation; both desktop and TUI recovery controls are now
implemented with their validation recorded above.

Desktop/TUI rollout authoring and readback passed the final full frontend suite
(67 files / 804 tests), zero-error/warning type checks and production build.
Final `make check` passed against the refreshed bundle
(`/tmp/lola-rollout-ui-make-check-final.log`); the preceding run exercised daemon
tests uncached in 183.437s. Focused tests cover complete order, UTF-8 limits,
frozen and uncertain authorization, retained drafts, overview isolation,
terminal paste and scrolling. An independent reviewer inspected 24 captures,
requested one error-visibility fix, then scored that fix resolved (`ship`) using
four additional focus captures and browser assertions. This was a verdict pass
on the named fix, not a new full-surface review. Documentation retained the
incumbent UI system. These results do not establish live pilot interoperability.

The stack rollout domain/CLI increment passed final `make check` after the
versioned authorization change (`/tmp/lola-rollout-check-wire-final.log`).
Migration race checks, new CLI/bridge/protocol checks and publication-template
recovery tests also pass. The generated rollout types pass frontend type
checking with zero errors or warnings. Temporary Git/fake-service tests show
rollout context reaching workers, repair planning and specialists, requirements
changing evidence identity, and lost publication replies recovering without
repeated writes. Version 1/2 migration fixtures retain populated usage records,
goal data and policy digests, including after failed writes and reopen. Rollout
authoring and display are now also implemented in desktop/TUI; their verification
is recorded below. A live pilot remains pending.

`make check` passed after the optional human acceptance implementation and final
receipt-preservation guard (build, vet and full Go suite; daemon tests ran
uncached). Focused daemon/CLI acceptance tests and goal/policy race checks passed.
The earlier launch-recovery regression also passed its focused and full checks.
Desktop/TUI acceptance controls passed focused behavior tests. The frontend now
passes 67 files / 801 tests, type checking with zero errors or warnings, and its
production build. Final `make check` passed after the client changes, with
frontend build and Go checks run sequentially to preserve embedded assets.
The independent acceptance UI review returned `ship` with no material fixes
across 24 desktop/narrow web and wide/compact terminal captures.
Desktop and TUI acceptance/graph/learning captures and UI review notes are in
[the evidence index](../.impeccable/review/README.md). Those are fixture captures,
not proof that the installed daemon supports the new commands.

The staged checkpoint is not committed: the configured 1Password Git signer
failed to complete the previous commit. Signing has not been bypassed. The
unrelated unstaged `updateservice.ts` comment edit remains outside this work.

## Pilot baseline and live environment preparation

[Retained pilot evidence](evaluation/nori-dialog-pilot/README.md) records the
unchanged Nori commit, pinned image and inputs, 10 passing unit tests, 3 passing
Storybook tests, a passing frontend build, and four font-verified browser cases
for desktop/narrow light/dark behavior. The unchanged baseline fails the new
style-token gate as expected. The final image also passes the real Docker
validation-boundary and timeout-cleanup test. A separate daemon exercised the
comparison CLI over its real socket and retained an explicitly unrun report;
it is now stopped. No Nori source, normal Lola installation or Linear object
was changed, and no model call started. This establishes pilot setup and
baseline evidence, not autonomous execution, recovery or measured improvement.
Container model authentication is pending.


### Live pilot findings — 2026-10-22

Parent NOR-493 and task NOR-494 are published under Dialog layout pilot in the
existing Nori project, with app routing and no initiative changes. One coordinator
and one worker ran. Restart recovered the same worker and call identities.
The worker passed the scoped unit/story/build/browser checks, but stopped before
submission at 285k observed tokens (cached input included) against the chosen
250k cap. No independent verification, specialist pass, integration or PR exists.

Generated Markdown now matches Linear list serialization and protects repository
paths as code spans. Fresh goal sandboxes seed Claude trust only for their
operator-authorized root. Confirmed worker termination now closes its usage-run
timestamp. Focused regressions and the final `make check` pass after all three fixes
(`/tmp/lola-nori-dialog-pilot/lola-check-final-fixes.log`). [Pilot evidence](evaluation/nori-dialog-pilot/README.md)
retains the exact limits, output, usage and restart observations. A larger token
allowance requires a fresh run; the existing authorization is retained unchanged.
