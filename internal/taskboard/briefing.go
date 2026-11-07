package taskboard

import "strings"

// Briefing is the shared operating contract for launches, new user items and
// CLI help. Keep these entry points in agreement, including already-live agents.
func Briefing(bin string) string {
	return strings.ReplaceAll(`## Shared tasks and requirements

The Overview task list is the durable agreement with the user. Your reported plan is a replaceable implementation checklist; it does not replace these tasks.

1. Read {{bin}} report task list before working, after a new user request, and before reporting completion. Preserve IDs and the user's exact scope. Reuse the most relevant existing task instead of creating a duplicate.
2. A task is one independently verifiable outcome. Use a concise action title. A requirement is an acceptance criterion or constraint, not another task. Record requirements from user messages and necessary criteria you discover; do not invent optional work or broaden scope.
3. You own organization. Attach each task-specific requirement to the task mainly responsible for satisfying it. Organize entries with an empty parentId and empty scope first. Use --global only for requirements that genuinely apply across the session, such as accessibility or compatibility constraints; these live in the Requirements tab. Do not duplicate a criterion under multiple tasks. Create a task only if no existing outcome fits. If intent is ambiguous, ask before changing scope.
4. Set a task to doing when work actually starts. Mark each requirement done only after verifying it, then mark its task done. Unmet requirements prevent completion. Adding, reopening or editing a requirement reopens a completed parent task. Do not mark children complete merely because their parent is complete.
5. Do not rewrite or remove user-authored entries. Clarifications belong in additional attached requirements. Move a misplaced requirement with attach, preserving its ID, text and author. Before removing your own task, move its requirements to a suitable task. If blocked, use report blocked and explain what is needed; never report done.
6. Update on meaningful changes, not every tool call. Re-read before finishing, verify global requirements too, and reconcile the list with what was actually delivered. Done is an agent report, not user approval or a signal to merge.

Commands (replace angle-bracket placeholders; use stable short IDs):
  {{bin}} report task list
  {{bin}} report task add <task-id> task "Implement the requested outcome"
  {{bin}} report task add <requirement-id> requirement --task <task-id> "Verifiable acceptance criterion"
  {{bin}} report task add <requirement-id> requirement --global "Session-wide constraint"
  {{bin}} report task attach <requirement-id> <task-id|--global>
  {{bin}} report task state <id> <todo|doing|done>
  {{bin}} report task edit <your-id> "Clarified text"
  {{bin}} report task remove <your-id>
  {{bin}} report task help

New agent requirements must include --task or --global. Human additions without either appear as unassigned in Requirements until you organize them. Commands return the current list as JSON; check errors and re-read after a conflict. Reporting alone never sends a message or changes the session lifecycle.
`, "{{bin}}", bin)
}
