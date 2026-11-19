export type PRChoice = { value: string; label: string };
export type PRSelection = { remote: string; sourceRef: string; sourceSHA: string; baseRepo: string; base: string };
export type PROptions = { sourceRepo: string; repositories: string[]; baseRepo: string; defaultBranch: string; branches: string[]; reviewers: PRChoice[]; assignees: PRChoice[]; labels: PRChoice[]; notices: string[] };
export type PRPreview = { sourceRepo: string; head: string; baseSHA: string; needsPush: boolean; commits: number; files: number; subject: string };
export type PRCreateRequest = { selection: PRSelection; title: string; body: string; draft: boolean; push: boolean; reviewers: string[]; assignees: string[]; labels: string[] };
export type PRResult = { number: number; url: string; existing: boolean; warnings: string[] };
export type PRDraft = { sourceRef: string; remote: string; baseRepo: string; base: string; title: string; body: string; draft: boolean; reviewers: string[]; assignees: string[]; labels: string[] };

// GitHub refuses very long URLs; the description is the only field that can get there.
const WEB_URL_MAX = 7000;
const refPath = (ref: string) => encodeURIComponent(ref).replace(/%2F/g, '/');

/**
 * GitHub's own "open a pull request" form, pre-filled. GitHub reads title,
 * body, assignees and labels from the query; it has no parameter for reviewers
 * or draft, so those are chosen on the page. A description too long for a URL
 * is cut, never dropped silently into a broken link.
 */
export function pullRequestWebURL(d: { baseRepo: string; base: string; sourceRepo: string; head: string; title: string; body: string; assignees: string[]; labels: string[] }): string {
  const fork = d.sourceRepo && d.sourceRepo.toLowerCase() !== d.baseRepo.toLowerCase();
  const head = (fork ? d.sourceRepo.replace('/', ':') + ':' : '') + refPath(d.head);
  const build = (body: string) => {
    const q = new URLSearchParams({ quick_pull: '1' });
    if (d.title) q.set('title', d.title);
    if (body) q.set('body', body);
    if (d.assignees.length) q.set('assignees', d.assignees.join(','));
    if (d.labels.length) q.set('labels', d.labels.join(','));
    return `https://github.com/${d.baseRepo}/compare/${refPath(d.base)}...${head}?${q}`;
  };
  let body = d.body, url = build(body);
  while (url.length > WEB_URL_MAX && body) { body = body.slice(0, Math.floor(body.length * 0.8)); url = build(body && body + '…'); }
  return url;
}
