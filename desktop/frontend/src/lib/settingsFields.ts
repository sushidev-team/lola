// Shared language for workspace defaults and per-project overrides.
export const PICKUP_FIELDS = {
  matchMode: "Label matching",
  dedupMode: "Repeat pickup",
  onSentSetLabel: "After pickup label",
};
export const LABEL_MATCHING = [
  { id: "any", label: "Any label" },
  { id: "all", label: "All labels" },
];
export const REPEAT_PICKUP = [
  { id: "label", label: "Change a label after pickup" },
  { id: "seen", label: "Remember picked-up issues" },
  { id: "state", label: "Use workflow state" },
];
export const ENTRY_FORMAT: Record<string, string> = {
  Symlinks: "One path per line.",
  "Post-create": "One command per line.",
  "Dev commands": "One command per line.",
  Env: "KEY=value, one per line.",
  "Match labels": "One UUID per line.",
  "Workflow states": "One UUID per line.",
  "Priority sort": "One sort key per line.",
};

export function settingsSaveError(error: unknown): { summary: string; description?: string; details?: string } {
  const message = String(error).replace(/^(?:(?:RuntimeError|Error):\s*)+/, '');
  const projects = new Set<string>();
  const other: string[] = [];
  for (const line of message.split('\n')) {
    const match = line.match(/^project "([^"]+)" polling: dedup_mode=label requires (?:on_sent_set_label|match_labels)\b/);
    if (match) projects.add(match[1]);
    else if (line.trim()) other.push(line);
  }
  if (!projects.size) return { summary: message };
  return {
    summary: `Label-based pickup needs labels for ${projects.size === 1 ? '1 project' : `${projects.size} projects`}.`,
    description: 'In Project defaults or the affected project, choose “Remember picked-up issues” under Repeat pickup, or configure Match labels and After pickup label.',
    details: [`Affected projects: ${[...projects].join(', ')}.`, ...other].join('\n'),
  };
}
