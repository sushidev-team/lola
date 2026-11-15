import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/svelte";

import Tasks from "./tasks.svelte";
import { store } from "$lib/store.svelte";
import { setTouchSurface } from "$lib/touchsurface";
import { copy } from "@mobile/lib/copy";
import { router } from "@mobile/lib/router.svelte";

// The task board as the phone's sheet: gutter body, segment-bar tabs, one empty block.

beforeEach(() => {
  setTouchSurface({});
  store.alive = true;
  store.sessions = [{ id: "s1", project: "p", agent: "claude", agentState: "idle", tasks: [] } as never];
  router.stack = [{ name: "tab", tab: "inbox" }, { name: "session", id: "s1", seg: "conversation" }];
  router.sheet = "tasks";
});

afterEach(() => {
  cleanup();
  setTouchSurface(null);
  store.sessions = [];
  router.sheet = "";
});

describe("tasks sheet", () => {
  it("keeps the 16pt gutter around the board", () => {
    const { container } = render(Tasks);
    expect(container.querySelector("[data-phone-desk]")!.className).toContain("px-gutter");
  });

  it("reads 'To do' and 'Requirements' and adds with a 44pt text button", () => {
    render(Tasks);
    expect(screen.getByRole("tab", { name: /To do/ })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: /Requirements/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add task" }).className).toContain("phone-add");
  });

  it("an empty board is a centred block with the title copy", () => {
    const { container } = render(Tasks);
    expect(screen.getByText(copy.tasks.empty.title)).toBeInTheDocument();
    expect(container.querySelector(".phone-empty svg")).not.toBeNull();
  });
});
