export type GitErrorInfo = { title:string; description:string; kind:'warn'|'bad'; recovery?:'pull'|'merge'|'refresh' };

/** Git stderr is diagnostic detail; the primary message explains the next step. */
export function gitErrorInfo(raw:string, action=''):GitErrorInfo {
  const text=raw.toLowerCase();
  if (/non-fast-forward|fetch first|tip of your current branch is behind/.test(text))
    return {title:'Push blocked',description:'The remote branch has new commits. Pull changes, then push again.',kind:'warn',recovery:'pull'};
  if (/not possible to fast-forward|divergent branches|branches have diverged|branch has diverged|cannot fast-forward/.test(text))
    return {title:'Branches have diverged',description:'Merge the remote changes, then push again.',kind:'warn',recovery:'merge'};
  if (/authentication failed|permission denied \(publickey\)|could not read username|terminal prompts disabled/.test(text))
    return {title:'Git sign-in required',description:'Check your Git credentials or SSH key, then try again.',kind:'bad'};
  if (/could not resolve host|could not resolve hostname|failed to connect|network is unreachable|connection timed out/.test(text))
    return {title:'Remote unavailable',description:'Check your connection, then try again.',kind:'bad'};
  if (/protected branch|gh006|gh013|repository rule violations|changes must be made through a pull request/.test(text))
    return {title:'Push blocked by repository rules',description:'Use a pull request or satisfy the required checks.',kind:'warn'};
  if (/automatic merge failed|merge conflict|unmerged files|resolve your current index/.test(text))
    return {title:'Conflicts need attention',description:'Resolve and stage the conflicted files, then continue.',kind:'warn'};
  if (/would be overwritten|commit or stash|local changes.*overwritten/.test(text))
    return {title:'Save your local changes first',description:'Commit or stash your changes, then try again.',kind:'warn'};
  if (/no upstream|has no upstream|upstream.*not configured/.test(text))
    return {title:'No upstream branch',description:'Publish this branch or choose its upstream first.',kind:'warn'};
  if (/changed since|stale (ref|head)|cannot lock ref.*expected/.test(text))
    return {title:'Repository changed',description:'Refresh and review the latest state before trying again.',kind:'warn',recovery:'refresh'};
  const clean=raw.replace(/^(?:(?:RuntimeError|Error):\s*|repository action failed:\s*)+/i,'').trim();
  const first=clean.split('\n').find(line=>line.trim()) ?? '';
  return {title:action ? `${action.charAt(0).toUpperCase()+action.slice(1).replaceAll('-',' ')} failed` : 'Git action failed',description:first.length>160 ? first.slice(0,157)+'…' : first || 'Try again or open the details for more information.',kind:'bad'};
}
