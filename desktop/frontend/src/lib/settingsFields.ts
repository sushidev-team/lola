// Shared language for workspace defaults and per-project overrides.
//
// Captions are plain words; the config.toml key a caption edits is secondary
// text (FIELD_KEYS), shown beside the caption for whoever reads the file.
export const PICKUP_FIELDS = {
  matchMode: "Label matching",
  dedupMode: "Avoid starting twice",
  onSentSetLabel: "Label to add when started",
};
export const LABEL_MATCHING = [
  { id: "any", label: "Any label" },
  { id: "all", label: "All labels" },
];
export const REPEAT_PICKUP = [
  { id: "label", label: "Add a label when an issue is started" },
  { id: "seen", label: "Remember started issues on this computer" },
  { id: "state", label: "Rely on the issue leaving its workflow state" },
];
export const ENTRY_FORMAT: Record<string, string> = {
  Symlinks: "One path per line.",
  "Post-create": "One command per line.",
  "Dev commands": "One command per line.",
  Env: "KEY=value, one per line.",
  "Match labels": "One Linear label ID per line.",
  "Workflow states": "One Linear workflow state ID per line.",
  "Priority sort": "One sort key per line.",
};
/** The config.toml key behind each caption, shown as secondary text. */
export const FIELD_KEYS: Record<string, string> = {
  [PICKUP_FIELDS.matchMode]: "match_mode",
  [PICKUP_FIELDS.dedupMode]: "dedup_mode",
  [PICKUP_FIELDS.onSentSetLabel]: "on_sent_set_label",
  "Match labels": "match_labels",
  "Workflow states": "state_ids",
};
