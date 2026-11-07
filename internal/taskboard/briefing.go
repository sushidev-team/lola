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
4. Set a task to doing when work actually starts. Mark each requirement done only after verifying it, then mark its task done. For a task or requirement that needs the user's manual check, set state checking (Needs your check), explain what to check, and wait for their explicit confirmation before marking it done. Never claim a user check yourself. Unmet requirements prevent completion. Adding, reopening or editing a requirement reopens a completed parent task. Do not mark children complete merely because their parent is complete.
5. Do not rewrite or remove user-authored entries. Clarifications belong in additional attached requirements. Move a misplaced requirement with attach, preserving its ID, text and author. Before removing your own task, move its requirements to a suitable task. If blocked, use report blocked and explain what is needed; never report done.
6. Update on meaningful changes, not every tool call. Re-read before finishing, verify global requirements too, and reconcile the list with what was actually delivered. Done is an agent report, not user approval or a signal to merge.
7. Feedback may arrive in the ordinary composer, with or without an item ID. Read task list, match it to the existing task or specific requirement, and record it with task feedback <id> "User feedback" before addressing it. This preserves scope and reopens the item. Do not create duplicate tasks for feedback. Ask if the target is ambiguous. When the user explicitly confirms a check in conversation, mark that item done and complete its parent only once all requirements are met. After a fix that needs another manual check, return the item to checking.

Commands (replace angle-bracket placeholders; use stable short IDs):
  {{bin}} report task list
  {{bin}} report task add <task-id> task "Implement the requested outcome"
  {{bin}} report task add <requirement-id> requirement --task <task-id> "Verifiable acceptance criterion"
  {{bin}} report task add <requirement-id> requirement --global "Session-wide constraint"
  {{bin}} report task attach <requirement-id> <task-id|--global>
  {{bin}} report task state <id> <todo|doing|checking|done>
  {{bin}} report task feedback <id> "User feedback to address"
  {{bin}} report task edit <your-id> "Clarified text"
  {{bin}} report task remove <your-id>
  {{bin}} report task help

New agent requirements must include --task or --global. Human additions without either appear as unassigned in Requirements until you organize them. Commands return the current list as JSON; check errors and re-read after a conflict. Reporting alone never sends a message or changes the session lifecycle.
`, "{{bin}}", bin)
}

// ConversationHint also reaches agents launched before the current briefing.
// Keep it short: it accompanies ordinary prose, never slash commands.
const ConversationHint = "[lola tasks] Read lola report task list. Route user feedback to its existing task/requirement with lola report task feedback <id> <text>; ask if ambiguous. Use lola report task state <id> checking for checks only the user can perform; mark those checks done only after their explicit confirmation. Complete a parent only after all its requirements are met."
