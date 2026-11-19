import { beforeEach, describe, expect, it } from "vitest";
import { nav } from "./nav.svelte";
import { store } from "./store.svelte";
import { gitTargetProject, toggleGitWorkspace } from "./workspacenav";

const repo = (name: string) => ({ name, label: name, path: `/r/${name}`, repo: `o/${name}`, kind: "repo" });

describe("gitTargetProject", () => {
  beforeEach(() => {
    store.projects = [repo("lola"), repo("nori"), repo("okane")] as never;
    store.sessions = [{ id: "lola-1", project: "lola" }] as never;
    nav.projectTab = "overview";
    nav.goCockpit("");
    nav.selectedId = "lola-1";
    nav.focusedTerm = "lola-1";
  });

  it("opens the open session's project when no project is highlighted", () => {
    expect(gitTargetProject()?.name).toBe("lola");
  });

  it("opens the highlighted project, not the open session's", () => {
    // Filtering inside the cockpit keeps the open session on screen.
    nav.scopeTo("nori");
    expect(nav.focusedTerm).toBe("lola-1");
    expect(gitTargetProject()?.name).toBe("nori");
    toggleGitWorkspace();
    expect(nav.inGit).toBe(true);
    expect(nav.project).toBe("nori");
  });

  it("prefers the open session's project among several highlighted ones", () => {
    nav.scopeTo("nori");
    nav.toggleScope("lola");
    expect(gitTargetProject()?.name).toBe("lola");
    nav.toggleScope("lola");
    nav.toggleScope("okane");
    expect(gitTargetProject()?.name).toBe("nori");
  });
});
