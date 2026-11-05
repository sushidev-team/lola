import { it, expect, beforeEach, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/svelte";
import CommandPalette from "./CommandPalette.svelte";
import { store, type ProjectInfo, type SessionInfo } from "$lib/store.svelte";
import { nav } from "$lib/nav.svelte";
beforeEach(() => {
 cleanup(); vi.restoreAllMocks();
 store.projects=[{name:"hlmw9",label:"Hlmw9 Website",path:"/code/website",repo:"acme/web",repoConfigured:true} as ProjectInfo];
 store.sessions=[{id:"a",title:"Investigate checkout",project:"hlmw9",worktree:"/code/website"} as SessionInfo];
 nav.openOverlay("commands");
});
it("finds a project by path and launches an action without navigating first", async () => {
 render(CommandPalette);
 await fireEvent.input(screen.getByRole("combobox"),{target:{value:"/code/web"}});
 await fireEvent.keyDown(screen.getByRole("combobox"),{key:"ArrowDown"});
 await fireEvent.keyDown(screen.getByRole("combobox"),{key:"Enter",metaKey:true});
 expect(nav.overlay).toBe("newsession"); expect(nav.overlayProject).toBe("hlmw9");
});
it("opens named sessions directly", async () => {
 render(CommandPalette);
 await fireEvent.input(screen.getByRole("combobox"),{target:{value:"checkout"}});
 await fireEvent.keyDown(screen.getByRole("combobox"),{key:"Enter"});
 expect(nav.selectedId).toBe("a"); expect(nav.focusedTerm).toBe("a"); expect(nav.overlay).toBeNull();
});
