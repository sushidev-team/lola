import { beforeEach, it, expect, vi } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/svelte";
import TemporarySessions from "./TemporarySessions.svelte";
import { nav } from "$lib/nav.svelte";
import { store } from "$lib/store.svelte";

beforeEach(() => { cleanup(); vi.restoreAllMocks(); store.sessions=[]; nav.closeOverlay(); });
it("opens the name and directory dialog without spawning a shell", async () => {
  const create = vi.spyOn(store,"openManual");
  render(TemporarySessions);
  await fireEvent.click(screen.getByRole("button",{name:"New temporary shell"}));
  expect(create).not.toHaveBeenCalled();
  expect(nav.overlay).toBe("newsession");
  expect(nav.overlayProject).toBe("");
  expect(nav.sessionLocation).toBe("temporary");
});
