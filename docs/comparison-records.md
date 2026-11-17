# Comparable-run records

`lola compare` turns an explicit evaluation plan and retained run evidence into a
reproducible JSON or Markdown report. It does not launch work, modify goal or
lesson stores, verify cited artifacts, or claim that more findings mean better
results. Records are portable files: keep the plan, filled records and reports
in your ordinary evidence directory or review branch. Preserve earlier versions.

The daemon must include the comparison commands. Installing a new binary and
restarting the daemon are separate from building source; an older daemon reports
an unknown command. All commands below print their result to stdout.

## Prepare before running

```sh
lola compare template > comparison-plan.json
# Fill the plan's task, repository bases, acceptance criteria and finite limits.
lola compare prepare comparison-plan.json > comparison-records.json
```

The template is intentionally incomplete. It cannot be reported as an experiment.
`prepare` requires an unrun plan and refuses to overwrite existing observations.
It produces a baseline and treatment draft for every declared task/repetition.
The drafts also cannot produce a report until actual outcomes and timestamps
are filled. Use different output files, as above, so shell redirection cannot
truncate your input.

Declare representative tasks and repeats up front. A pair is one task/repetition,
not an entire collection of unlike tasks. Its fields are:

| Field | Meaning |
| --- | --- |
| `id` | Stable pair ID; use a different ID for every repeat. |
| `task` | The same concrete outcome for both arms. |
| `acceptance` | Observable checks applied equally to both arms. |
| `repositories` | Repository names and full starting commit hashes, including all stack members. |
| `limits` | Shared finite `agent_calls`, `runtime_seconds` and `tokens` ceilings; `spend_usd: 0` means no additional spend ceiling. |
| `intervention` | Exactly which lessons/reviewers are enabled in treatment and absent or different in baseline. |

Keep execution settings and acceptance checks fixed except for the declared
intervention. Run both arms from independent copies of the recorded bases; do
not let treatment see baseline changes, feedback or assessment notes. Alternate
or randomize arm order and retain that choice with the task evidence. Use the
same evaluator/checks where possible. Starting timestamps retain actual order.
Count coordinator, worker and reviewer calls in the same shared ceiling.

Each generated run has a `pair_sha256` binding its task, bases, criteria,
intervention and budget. Changing the plan after collecting results invalidates
those bindings. Create another comparison rather than rewriting an old plan to
fit its results. Retain the original planned pairs even if an arm never starts;
remove only its unfilled run draft, so the report shows that arm as missing.
An interrupted run that did start should remain as `outcome: "incomplete"` with
its actual stop time and evidence, not be removed.

## Record actual runs

Each run requires `id`, `pair_id`, `arm`, the prepared `pair_sha256`,
`started_at`/`ended_at` timestamps and an outcome (`accepted`, `rejected` or
`incomplete`) with `outcome_evidence` references. Acceptance is an explicit
assessment against the predeclared criteria; a worker's “done” message is not
acceptance evidence. Cite checks, commits and captured interaction artifacts.

Record the worker's `agent`, `requested_model`, `resolved_model`,
`prompt_sha256` and `source` reference to the retained invocation/prompt.
Do not infer a resolved model from an alias or default. Leave unknown model or
prompt fields empty and explain them in `unknown_reason`. Prompt hashes preserve
identity, but do not replace retaining the exact prompt at the source reference.
The report flags worker/model/prompt differences and unknown provenance.

For every supplied lesson record `id`, exact `version`, content `sha256` and
`source`. For every reviewer retain `profile`, `provider`, `rubric_version` and
its own `invocation` with the same provenance fields as the worker. Empty arrays
mean no lessons/reviewers were supplied; they do not mean “not recorded”. Recover
that information from the retained invocation before assessing the run. A source
reference is not fetched or independently verified by the report command.

Record `agent_calls`, `tokens` and every entry in `metrics` as a measurement:

```json
{
  "value": 45,
  "status": "observed",
  "evidence": ["artifacts/operator-timing.csv#run-treatment" ]
}
```

A measured zero uses `value: 0` and still needs evidence. Unavailable data uses:

```json
{
  "value": null,
  "status": "unavailable",
  "evidence": [],
  "reason": "Operator intervention time was not tracked for this run."
}
```

Statuses are `observed`, `reported`, `estimated` and `unavailable`. They describe
the supplied evidence, not independent certification by Lola. Use `reported`
for a recorded human assessment, `estimated` for an approximation, and
`unavailable` when there is no defensible value. Partial usage logs are not proof
of complete token/call consumption; retain their limitation rather than labeling
a lower bound as complete observed usage.

| Metric key | Recording rule |
| --- | --- |
| `repeat_corrections` | Distinct repeated mistakes that require another correction, with links to the original and repeated event. Deduplicate repeated messages about one occurrence. |
| `useful_findings` | Distinct findings explicitly assessed as actionable/correct; cite the assessment. |
| `dismissed_findings` | Distinct findings explicitly dismissed with a reason; keep these separate from unassessed claims. |
| `unassessed_findings` | Distinct findings without a completed assessment. Zero needs a completed review of the finding set. |
| `regressions_caught` | Reproducible regressions caught before human review; retain the failing check, fix and timing evidence. Finding count alone is insufficient. |
| `human_intervention_seconds` | Active operator time spent correcting, unblocking or steering, using a timer/log or explicitly labeled estimate. Exclude unattended runtime; count consistently in both arms. |
| `cost_usd` | Total run cost, including coordinator, worker and specialist calls. Retain whether it is observed, estimated or unavailable. Resource ceilings and token counts are not dollar measurements. |

Counts must be nonnegative integers; time and cost may be fractional. Every
metric must be present, even when unavailable. Source references must exclude
credentials and private user data. Schema version 1 accepts at most 100 planned
pairs / 200 run records in a 512 KiB input. Split larger experiments into smaller
predeclared cohorts, retaining each cohort's plan and selection rule.

## Generate and interpret

```sh
lola compare report comparison-records.json > comparison-report.json
lola compare report comparison-records.json --markdown > comparison-report.md
```

The normalized input bundle SHA-256 and pair contract hashes make reports
reproducible from retained records. No timestamp is generated during reporting,
so rerunning identical records produces identical output. Unknown fields and
schema versions, duplicate run slots, changed contracts and malformed metrics
are rejected instead of being silently interpreted as evidence.

Every planned pair appears in the report. Aggregates use only pairs with both
runs and recorded consumption within the shared call, runtime, token and
optional spend ceilings. Unknown or estimated call/token usage cannot establish
that condition; an estimated spend cannot establish an enabled spend ceiling.
The report retains the exclusion reasons. Excluding unfinished or poorly
measured work can bias results, so report these exclusions alongside the totals.

Totals and treatment-minus-baseline differences are unavailable if any included
measurement is unavailable. Known subtotals and evidence-status counts remain
visible without substituting for missing totals. Cost per accepted task includes
costs of rejected and incomplete runs in the matched cohort, divided by the
number accepted in that arm. It is unavailable with no accepted tasks or any
missing included cost; estimates remain labeled in measurement coverage.

Matched budgets do not prove causality. Worker/model changes, prompt changes,
unknown resolved models and missing intervention differences are flagged.
Inspect prompt differences to establish whether only the intended lesson/review
context changed. Useful and dismissed findings are assessment counts, not
precision/recall or model accuracy. Report sample size, task selection, run order,
excluded pairs and evaluator limitations; do not claim improvement from totals
alone.

Automated tests use explicitly synthetic fixtures. They verify calculations,
identity checks, missing-data behavior and command boundaries. They are not
representative live comparisons or evidence that Lola improves outcomes.
