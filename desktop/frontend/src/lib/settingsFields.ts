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
