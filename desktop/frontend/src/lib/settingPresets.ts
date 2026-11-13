export type Preset = { value: string; label: string };

// Verified 2026-09-05. These are suggestions, never a validation allowlist.
// Claude aliases track the configured provider's current release:
// https://code.claude.com/docs/en/model-config
export const CLAUDE_MODELS: Preset[] = [
  { value: "", label: "Use agent default" },
  { value: "sonnet", label: "Sonnet — balanced" },
  { value: "opus", label: "Opus — complex reasoning" },
  { value: "haiku", label: "Haiku — fast" },
  { value: "fable", label: "Fable — long-running tasks" },
  { value: "best", label: "Best available" },
];
// https://learn.chatgpt.com/docs/models
export const CODEX_MODELS: Preset[] = [
  { value: "", label: "Use agent default" },
  { value: "gpt-6-astra", label: "GPT-6 Astra" },
  { value: "gpt-5.6-sol", label: "GPT-5.6 Sol" },
  { value: "gpt-5.6-terra", label: "GPT-5.6 Terra" },
  { value: "gpt-5.6-luna", label: "GPT-5.6 Luna" },
  { value: "gpt-5.5", label: "GPT-5.5" },
];
export const modelsFor = (agent: string): Preset[] => agent === "claude" ? CLAUDE_MODELS
  : agent === "codex" ? CODEX_MODELS : [{ value: "", label: "Use agent default" }];
export const POLL_INTERVALS = ["30s", "60s", "2m", "5m"].map((value) => ({ value, label: value }));
export const BRANCH_PREFIXES: Preset[] = [
  { value: "", label: "Use default" },
  ...["lola/", "feature/", "fix/"].map((value) => ({ value, label: value })),
];
export const CLAUDE_BINARIES: Preset[] = [
  { value: "", label: "Default (Claude on PATH)" },
  { value: "claude", label: "claude" },
];
export const BASE_FLAGS: Preset[] = [
  { value: "--base", label: "--base" },
  { value: "-b", label: "-b" },
  { value: "", label: "Do not pass a base branch" },
];
