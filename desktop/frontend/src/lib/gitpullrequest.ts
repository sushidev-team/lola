export type PRChoice = { value: string; label: string };
export type PRSelection = { remote: string; sourceRef: string; sourceSHA: string; baseRepo: string; base: string };
export type PROptions = { sourceRepo: string; repositories: string[]; baseRepo: string; defaultBranch: string; branches: string[]; reviewers: PRChoice[]; assignees: PRChoice[]; labels: PRChoice[]; notices: string[] };
export type PRPreview = { sourceRepo: string; head: string; baseSHA: string; needsPush: boolean; commits: number; files: number; subject: string };
export type PRCreateRequest = { selection: PRSelection; title: string; body: string; draft: boolean; push: boolean; reviewers: string[]; assignees: string[]; labels: string[] };
export type PRResult = { number: number; url: string; existing: boolean; warnings: string[] };
export type PRDraft = { sourceRef: string; remote: string; baseRepo: string; base: string; title: string; body: string; draft: boolean; reviewers: string[]; assignees: string[]; labels: string[] };
