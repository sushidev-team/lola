import { base, project, ago } from "./build";
import type { GitCommit, GitRef, GitSnapshot } from "$lib/projectgit";

const repo = "sushidev-team/lola";
const topic = "release-please--branches--main";
function commit(sha: string, parents: string[], subject: string, minutes: number, author = "Martin"): GitCommit {
  return { sha, parents, subject, author, email: "", date: ago(minutes), refs: [] };
}
const commits = [
  commit("9f97d1f", ["9d760af"], "Adjust conversation spacing", 3),
  commit("9d760af", ["78e9d56"], "Reserve the first lane for main", 8),
  commit("427ac8a", ["78e9d56"], "Prepare the next release", 11, "github-actions"),
  commit("78e9d56", ["84eb3c6", "63217fb"], "Merge the iOS Git client", 15),
  commit("63217fb", ["4bb2026", "243526e"], "Bring branch history to iPhone", 18),
  commit("243526e", ["8caab02"], "Cover the mobile diff viewer", 24),
  commit("4bb2026", ["8caab02"], "Keep remote history read-only", 30),
  commit("84eb3c6", ["8caab02"], "Refine project navigation", 42),
  commit("8caab02", [], "Add the project Git workspace", 60),
];
function ref(name: string, index: number, remote = false): GitRef {
  return { name, fullName: (remote ? "refs/remotes/" : "refs/heads/") + name, remote, current: !remote && name === "main", sha: commits[index].sha };
}
const snapshot: GitSnapshot = {
  path: "/Users/dev/code/lola", branch: "main", head: commits[0].sha, upstream: "origin/main",
  commits, refs: [{ ...ref("main", 0), upstream: "refs/remotes/origin/main", ahead: 2 }, ref("origin/" + topic, 2, true), ref("origin/main", 3, true)],
  remotes: [{ name: "origin", host: "github.com", owner: "sushidev-team", repo, avatarUrl: "" }],
  files: [], worktrees: [], signing: { enabled: false, key: "", format: "" }, hasMore: false,
};
export const fixture = base("graph", {
  routes: ["/projects/lola/graph", "/projects/lola/git"],
  projects: [project("lola", "Lola")], git: { lola: snapshot },
  prs: { repo, ageSeconds: 0, stale: false, prs: [{
    number: 90, title: "Prepare the next release", author: "github-actions", branch: topic, isDraft: false, isFork: false,
    checks: "pass", review: "", url: "https://github.com/" + repo + "/pull/90", status: "review_pending", alreadyOpen: false,
  }] },
});
