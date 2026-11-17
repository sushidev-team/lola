import {describe, it, expect} from 'vitest';
import {pairedBranch, branchPRs, type BranchPR} from './gitrefdisplay';
import type {GitRef} from './projectgit';

const remotes = [{name:'origin',repo:'me/fork',host:'github.com',owner:'me',avatarUrl:''},{name:'upstream',repo:'team/repo',host:'github.com',owner:'team',avatarUrl:''}];
const ref = (name:string, remote=false, sha='a'):GitRef => ({name,remote,sha,fullName:`refs/${remote?'remotes':'heads'}/${name}`,current:false});
const local = {...ref('topic'),upstream:'refs/remotes/origin/renamed'};
const tracked = ref('origin/renamed',true), other = ref('upstream/topic',true);
const pr:BranchPR = {number:1,title:'Change',url:'https://github.com/team/repo/pull/1',repo:'team/repo',headRepo:'ME/FORK',head:'renamed',base:'main',draft:false};

describe('Branch identity display',()=>{
  it('pairs the tracked upstream even when branch names differ',()=>{
    expect(pairedBranch(local,[local,other,tracked],remotes)).toBe(tracked);
    expect(pairedBranch(local,[other,{...tracked,sha:'b'}],remotes)).toBeUndefined();
  });
  it('combines untracked same-name tips but never merely similar names',()=>{
    const branch=ref('topic');
    expect(pairedBranch(branch,[ref('origin/other/topic',true)],remotes)).toBeUndefined();
    expect(pairedBranch(branch,[other],remotes)).toBe(other);
    expect(pairedBranch(other,[branch],remotes)).toBeUndefined();
  });
  it('matches PRs by source repository and tracked branch, independently of tip SHA',()=>{
    const prs=[pr,{...pr,number:2,headRepo:'other/fork'}, {...pr,number:3,head:'topic'}];
    expect(branchPRs(local,[{...tracked,sha:'b'}],remotes,prs)).toEqual([pr]);
    expect(branchPRs(tracked,[],remotes,prs)).toEqual([pr]);
    expect(branchPRs(other,[],remotes,prs)).toEqual([]);
    expect(branchPRs(tracked,[],remotes.map(remote=>({...remote,host:'gitlab.com'})),prs)).toEqual([]);
    expect(branchPRs(ref('renamed'),[tracked],remotes,prs)).toEqual([]);
  });
});
