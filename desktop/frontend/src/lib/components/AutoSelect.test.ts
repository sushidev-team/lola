import { beforeEach, it, expect } from "vitest";
import { render, cleanup } from "@testing-library/svelte";
import { tick } from "svelte";
import AutoSelect from "./AutoSelect.svelte";
import { openSession } from "$lib/projectActions";
import { nav } from "$lib/nav.svelte";
import { store, type SessionInfo } from "$lib/store.svelte";

beforeEach(() => {
  cleanup(); nav.goCockpit(); nav.setTriage(""); nav.select("old");
  store.sessions = [{id:"old",project:"acme",status:"shell"} as SessionInfo];
});

it("keeps the newly opened session selected while snapshots are still stale", async () => {
  render(AutoSelect);
  openSession("new");
  await tick();
  expect(nav.selectedId).toBe("new");
  expect(nav.focusedTerm).toBe("new");
  store.sessions = [...store.sessions];
  await tick();
  expect(nav.selectedId).toBe("new");
  store.sessions = [...store.sessions, {id:"new",project:"",status:"shell"} as SessionInfo];
  await tick();
  expect(nav.selectedId).toBe("new");
});

it("still selects a visible row when no terminal is focused", async () => {
  nav.select("missing");
  render(AutoSelect); await tick();
  expect(nav.selectedId).toBe("old");
});
