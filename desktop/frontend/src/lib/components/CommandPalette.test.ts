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

it("keeps the provider-specific CodeRabbit action in command search", async () => {
 store.sessions=[{id:'a', title:'Investigate checkout', project:'hlmw9', prNumber:7} as SessionInfo];
 nav.focusedTerm='a';
 const poll=vi.spyOn(store,'coderabbit').mockResolvedValue(undefined);
 render(CommandPalette);
 await fireEvent.input(screen.getByRole('combobox'),{target:{value:'CodeRabbit'}});
 expect(screen.getByRole('option', { name: /Poll CodeRabbit reviews/ })).toBeInTheDocument();
 await fireEvent.keyDown(screen.getByRole('combobox'),{key:'Enter'});
 expect(poll).toHaveBeenCalledWith('a');
 expect(nav.overlay).toBeNull();
});

it("opens the Team view and syncs the hub from the palette", async () => {
 const { hub } = await import("$lib/hub.svelte");
 const sync = vi.spyOn(hub, "sync").mockResolvedValue({ ok: true, data: { queued: true } });
 const flash = vi.spyOn(store, "setFlash");
 render(CommandPalette);
 await fireEvent.input(screen.getByRole("combobox"),{target:{value:"Team: Sync"}});
 expect(screen.getByRole("option", { name: /Team: Sync now/ })).toBeInTheDocument();
 await fireEvent.keyDown(screen.getByRole("combobox"),{key:"Enter"});
 expect(sync).toHaveBeenCalled();
 await vi.waitFor(() => expect(flash).toHaveBeenCalledWith("Team hub sync queued", "good"));
 cleanup(); nav.openOverlay("commands");
 render(CommandPalette);
 await fireEvent.input(screen.getByRole("combobox"),{target:{value:"Team: Open"}});
 await fireEvent.keyDown(screen.getByRole("combobox"),{key:"Enter"});
 expect(nav.view).toBe("team"); expect(nav.overlay).toBeNull();
});
