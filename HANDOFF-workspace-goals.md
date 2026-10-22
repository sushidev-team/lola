# Cross-device handoff: self-improving workspaces and goals

Updated 2026-10-22. Continue on `feat/self-improving-workspaces` in
`sushidev-team/lola`. Implementation checkpoints are `772dafc` and `468bd03`;
this handoff is committed immediately after them. **The full plan is not complete.**

## Latest user instruction and authorization

The user approved the full implementation plan and a live Nori dialog pilot.
They then approved another **20 minutes, an eight-minute worker attempt, and up
to three model calls**, retaining the existing aggregate **2M-token / $3** ceilings.
Before that new run started, they asked to stop here, push everything and hand off
to another device. No model call or new pilot execution occurred after this latest
approval; only read-only preparation occurred. Continue from this authorization
without asking for the same permission again. Do not start work on both devices.

Previously observed usage across all pilot attempts: **1,224,631 tokens**, including
cache, **five model calls**, **$0.8061666 estimated spend**. Remaining allowance:
**775,369 tokens, $2.1938334, three calls**. Telemetry is partial/delayed, not an exact
billing meter. Reserve one worker and both required specialist/final review calls;
do not add a coordinator call merely to recreate the already-reviewed one-task plan.
If these limits prevent delivery, retain the actual result; never silently increase
them, bypass reviews or merge into main.

## Start here

1. Fetch and check out `feat/self-improving-workspaces` from origin. In an existing
   checkout, inspect local changes first; do not reset, stash or overwrite them.
2. Read [AGENTS.md](AGENTS.md), [the plan](PLAN-self-improving-workspace.md),
   [the requirement audit](docs/workspace-goals-audit.md), and
   [the pilot record](docs/evaluation/nori-dialog-pilot/README.md).
3. Use [the implementation guide](docs/workspace-goals.md) for supported CLI/UI
   operations. Build with `make build`; the normal installed daemon is a separate
   binary. Run this experiment with an isolated `LOLA_HOME`, tmux socket and hook
   port. Do not replace or restart the user's ordinary daemon as a side effect.
4. Reconstruct the sandbox on the new device and preflight the exact complete
   acceptance command before spending the remaining agent calls. Then start the
   approved additional run with a deadline and eight-minute worker allowance.

The old isolated daemon is stopped, its socket is absent, and the workers were
stopped. The latest source checkout was clean. No Nori PR was opened or main
branch changed. Lola's implementation was committed; no production installation
was performed. The newest memory-limit implementation was tested but not installed
into the old pilot daemon.

## What is implemented and verified

Core goal phases 0–4, learning/review controls, desktop/TUI planning and
authorization, Linear synchronization, isolated execution, verification and
delivery controls have automated evidence. Outcome/comparison recording is
implemented. The audit is more reliable than an approximate percentage.

`468bd03` adds bounded acceptance-check `memory_mib` (256–8192; omitted/zero
retains 2048 MiB), policy/receipt binding, Docker enforcement, per-run usage and
desktop/TUI authorization controls. Old authorization digests remain unchanged.
Validation passed: `make check`, focused TUI authorization tests, six desktop
authorization tests, Svelte check (zero errors/warnings), production frontend build.

Earlier pilot fixes in `772dafc`: Linear Markdown normalization with protected
repository paths, Claude trust initialization for the authorized sandbox root,
and finished usage timestamps after confirmed worker stopping.

Still unfinished: one live pilot through independent verification, specialist
review, goal integration and a final draft PR; then representative matched-budget
comparisons. No measured quality-improvement claim is supported yet. Human merge
and, where required, human acceptance remain separate from pilot draft delivery.

## Live pilot history — preserve it

Use the existing Linear **Nori project**, repository labels and **Dialog layout
pilot** milestone. The user explicitly does not want initiatives or a new project.

| Goal | Linear parent / task | Actual result |
| --- | --- | --- |
| `d2d2db8a-3f28-43c6-ae11-0eb272a09b9f` | NOR-493 / NOR-494 | First run stopped at selected 250k token cap; 285k observed. Worker checks passed; no submission. Restart reused the existing attempt. |
| `95b9671e-e5ba-40c1-86f0-4481f7fc59b0` | NOR-495 / NOR-497 | Worker submitted; independent unit/story/browser checks passed, build exhausted Node heap. Integration rejected. Automatic second attempt was stopped to avoid wasted calls. |
| `33eef852-20b7-40bf-8614-61342cd94327` | NOR-498 / NOR-499 | Corrected-heap worker passed all checks and committed, but three-minute attempt deadline stopped it before accepted submission. |

The last worker commit is `557109cdb7cf79c42e813390292a509b5e452cd0` in a private
local pilot repository, **not pushed to Nori**. Its portable
[patch](docs/evaluation/nori-dialog-pilot/recovery/candidate.patch), checks and
screenshots are committed here. The prior submitted worker was
`71969919f32e6f9e84575b63557fdaa8b7fb3ebd`, independently checked as candidate
`66fa02bb78d85f71c15dda5f2689cc6339aa7df8`; its evidence is in `continuation/`.
Do not label either as integrated or delivered, or edit old receipts to resume it.

Publication sometimes created a reserved issue successfully but its immediate
collection lookup returned no result. Read the reserved UUID before retrying.
Reconcile those same IDs; do not generate replacement IDs or duplicate parents.
The root cause of the read visibility delay was not established.

Existing executions have immutable policies and exhausted deadlines. Current
supported recovery therefore needs a new goal/execution authorization. Reusing
the same milestone is supported. A future explicit run-renewal operation under
the same goal/Linear parent would reduce clutter, but is not implemented. Do not
invent an unsupported renewal command or directly mutate persisted policies.

## Portable setup and exact next run

Committed setup/evidence lives in `docs/evaluation/nori-dialog-pilot/`:

- `Dockerfile`, `setup-deps.sh`, `check.sh`, `capture.cjs`, `image-inputs.json`.
- `pilot-config.example.toml`: credential-free isolated-daemon configuration;
  replace absolute paths, image digest and architecture-specific binary path.
- `next-authorization.template.json`: remaining budget and reviewed acceptance
  command; replace goal ID, revision, checkout path and rebuilt image digest.
  It is deliberately not executable as-is (`revision: 0`, placeholder ID/path).
- `recovery/recovery-draft.json`: previously reviewed one-task scope. Update its
  historical time-limit prose to match the newly approved additional run; keep
  acceptance, repository paths and non-goals. Creating/publishing it does not
  itself authorize execution.
- `continuation/`, `recovery/`, `baseline/`, `stopped-worker/` and SHA-256 manifests:
  portable evidence, not an importable live-daemon state directory.

The old image was Linux ARM64:
`sha256:d3f85088fc383f8114b9ed1e57ba8916a0c92d75fba8c914d3a7085da0d7d6c8`.
It is local to the old machine, not pushed to a registry. Rebuild for the new
device and pin its actual digest. Dockerfile inputs include Nori's package
manifests and selected vendor frontend assets listed in `image-inputs.json`;
the README describes their source versions. The Dockerfile uses UID/GID 501:20;
adapt ownership to the new host where needed, then verify the exact boundary.
Build Lola's Linux sandbox binary for the image architecture as well.

Actual Nori base was `f94d30d7c8f08178c14297d67756a014d3ff089c`. The prepared
baseline was `97fc399c591d82cfcf54859fa0c0191311cb16b9`; only release metadata
changed between them. Recheck the new device's source revision and dependencies;
do not assume current main still matches either.

Acceptance command verified in the recovery worker:

```sh
NODE_OPTIONS=--max-old-space-size=1792 PILOT_ASSERT_STYLE=1 sh /opt/pilot/check.sh
```

Keep the 2 GiB container boundary for that proven setting unless a separately
reviewed authorization changes it. Default Node heap (~1 GiB) failed; 1536 MiB
also failed; 1792 MiB passed a separate same-boundary build and the recovery's
complete worker suite. The next run must still earn independent receipts.
The harness uses no network, a read-only root, 2 CPUs, bounded processes/scratch,
and disposable repository snapshots. It runs unit tests, Storybook tests,
TypeScript/Vite build and four browser theme/viewport cases with synthetic overflow.

The intended change is small: shared dialog scrim and shadow use existing tokens;
preserve Radix focus/Escape behavior, gutters, scrolling and invoice payloads.
Reuse prior findings as context rather than broadening scope. Do not submit on
behalf of a worker, manually mark a check passed, or skip specialist review to
meet the budget. Leave enough time after worker completion for independent checks
and both task/final reviews.

Relevant Linear IDs are already in the config/template: team, project, workflow
states, app label and milestone. Ordinary polling must remain disabled, with no
`agent-ready` label on pilot tasks. Only the model credential enters worker
containers; GitHub/Linear credentials and host home directories do not.

## Credentials and local-only state

Credentials are **not in Git or this handoff**. The old machine's
`/tmp/lola-pilot-claude-token`, keychain credentials, `/tmp/lola-nori-dialog-pilot`,
Docker images and private worktrees will not appear after a clone. Obtain valid
credentials through the new device's supported secure setup; never paste tokens
into chat, log them, commit them or copy an entire home directory. The previous
token was kept in a mode-600 local file and passed only through the daemon's child
environment. GitHub/Linear authentication must also work on the new machine.

Do not blindly copy old runtime JSON into a new Lola home: it contains old
absolute paths, process identities and stopped histories. Use committed evidence
for context and supported CLI operations for the fresh run.

## Delivery and reporting

[Screenshot Page](https://chatgpt.com/space/page_1288680f36c4819197328af1051006a8)
contains before/candidate images from the independently checked continuation and
an honest failure summary. It is private until shared. Update it with the actual
new independent artifacts before attaching it with `goal delivery-evidence`.
It shows Storybook, not authenticated invoice-page captures.

Follow the repository's `github-pr` skill for any PR creation/update: conventional
title, proportional description, real test outcomes, screen-specific manual checks,
screenshot artifact link and private-until-shared note. The end state is a draft
PR on a goal branch, never an automatic merge. Update the implementation plan and
audit with actual outcomes, retain evidence and commit the next checkpoint.

Suggested opening prompt on the other device:

> Read HANDOFF-workspace-goals.md on feat/self-improving-workspaces. Continue the
> already-authorized bounded Nori pilot from the handoff, preserving prior records
> and aggregate limits. Do not merge into main or restart the ordinary daemon.
