import type {GitRef, GitRemote} from './projectgit';

export type BranchPR = {number:number; title:string; url:string; repo:string; headRepo:string; head:string; base:string; draft:boolean};

export function branchRemote(branch: GitRef, remotes: GitRemote[]): GitRemote | undefined {
  return branch.remote ? remotes.filter(remote => branch.name.startsWith(`${remote.name}/`)).sort((a,b) => b.name.length-a.name.length)[0] : undefined;
}

export function pairedBranch(local: GitRef | undefined, tips: GitRef[], remotes: GitRemote[]): GitRef | undefined {
  if (!local || local.remote) return;
  // Explicit tracking wins, including differently named upstream branches.
  if (local.upstream) return tips.find(ref => ref.remote && (ref.fullName === local.upstream || ref.name === local.upstream) && ref.sha === local.sha);
  return tips.find(ref => ref.remote && ref.sha === local.sha && ref.name === `${branchRemote(ref,remotes)?.name}/${local.name}`);
}

export function branchPRs(branch: GitRef, refs: GitRef[], remotes: GitRemote[], prs: BranchPR[]): BranchPR[] {
  const source = branch.remote ? branch : refs.find(ref => ref.remote && (ref.fullName === branch.upstream || ref.name === branch.upstream));
  if (source) {
    const remote = branchRemote(source, remotes);
    return remote?.host === 'github.com' ? prs.filter(pr => pr.headRepo.toLowerCase() === remote.repo.toLowerCase() && pr.head === source.name.slice(remote.name.length+1)) : [];
  }
  // An unpublished branch has no reliable GitHub identity yet.
  return [];
}
