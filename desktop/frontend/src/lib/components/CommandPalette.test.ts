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

it("shows only common actions until typing, then matches sessions and projects first", async () => {
 render(CommandPalette);
 expect(screen.queryByText("Go to…")).toBeNull();
 const idle = screen.getAllByRole("option").map(o => o.textContent ?? "");
 expect(idle[0]).toContain("New session");
 expect(idle.some(t => t.includes("Investigate checkout") || t.includes("Hlmw9 Website"))).toBe(false);
 expect(screen.queryByRole("option", { name: /Doctor/ })).toBeNull();
 await fireEvent.input(screen.getByRole("combobox"),{target:{value:"o"}});
 const text = screen.getAllByRole("option").map(o => o.textContent ?? "");
 expect(text[0]).toContain("Investigate checkout");
 expect(text[1]).toContain("Hlmw9 Website");
 await fireEvent.input(screen.getByRole("combobox"),{target:{value:"Doctor"}});
 expect(screen.getByRole("option", { name: /Doctor/ })).toBeInTheDocument();
});

it("shows the selected entry's actions in an options panel on ⌘K", async () => {
 render(CommandPalette);
 await fireEvent.input(screen.getByRole("combobox"),{target:{value:"checkout"}});
 await fireEvent.keyDown(screen.getByRole("combobox"),{key:"k",metaKey:true});
 expect(screen.getByRole("menu", { name: /Investigate checkout actions/ })).toBeInTheDocument();
 expect(screen.getByRole("menuitem", { name: /Open session/ })).toBeInTheDocument();
 await fireEvent.keyDown(screen.getByRole("combobox",{name:"Search for actions"}),{key:"k",metaKey:true});
 expect(screen.queryByRole("menu")).toBeNull();
});

it("keeps Tab inside the dialog and exposes the active action", async () => {
 render(CommandPalette);
 const input = screen.getByRole("combobox", { name: /Search projects/ });
 await vi.waitFor(() => expect(input).toHaveFocus());
 const shift = await fireEvent.keyDown(input, { key: "Tab", shiftKey: true });
 expect(shift).toBe(false);
 expect(screen.getByRole("button", { name: /Actions/ })).toHaveFocus();
 await fireEvent.input(input, { target: { value: "checkout" } });
 await fireEvent.keyDown(input, { key: "k", metaKey: true });
 const box = screen.getByRole("combobox", { name: "Search for actions" });
 expect(box.getAttribute("aria-activedescendant")).toBe("command-option-0");
 expect(document.getElementById("command-option-0")).toBeInTheDocument();
});

it("returns focus to the invoking control when closed", async () => {
 const opener = document.createElement("button"); document.body.append(opener); opener.focus();
 const { unmount } = render(CommandPalette);
 await vi.waitFor(() => expect(document.activeElement).not.toBe(opener));
 nav.closeOverlay(); unmount();
 expect(opener).toHaveFocus(); opener.remove();
});
