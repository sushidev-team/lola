import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/svelte";

import Model from "./model.svelte";
import { store } from "$lib/store.svelte";
import { talk } from "$lib/talk.svelte";
import { copy } from "@mobile/lib/copy";
import { router } from "@mobile/lib/router.svelte";
import { tiers } from "@mobile/lib/tiers.svelte";

// The model sheet. It used to open blank: the desktop composer rule that hides
// `[data-model-settings]` also matched the sheet (order B scopes it), and an agent with
// no settings drew nothing at all. Now rows or an explanation, never nothing.

function open(over: Record<string, unknown>): void {
  store.alive = true;
  store.sessions = [{ id: "s1", project: "p", agent: "claude", agentState: "idle", ...over } as never];
  router.stack = [{ name: "tab", tab: "inbox" }, { name: "session", id: "s1", seg: "conversation" }];
  router.sheet = "model";
}

beforeEach(() => {
  talk.state("s1");
});

afterEach(() => {
  cleanup();
  store.sessions = [];
  router.sheet = "";
});

describe("model sheet", () => {
  it("lists the agent's options as rows", () => {
    open({});
    render(Model);
    expect(screen.getAllByRole("menuitemradio").length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByText(copy.model.unavailable.title)).toBeNull();
  });

  it("says to change on the Mac when this phone cannot send settings", () => {
    tiers.set(["read"]);
    open({});
    render(Model);
    expect(screen.getByText("Change on your Mac.")).toBeInTheDocument();
    tiers.clear();
  });

  it("is quiet about it when the phone may send settings", () => {
    tiers.set(["read", "answer"]);
    open({});
    render(Model);
    expect(screen.queryByText("Change on your Mac.")).toBeNull();
    tiers.clear();
  });

  it("says there is nothing to change for an agent with no settings", () => {
    open({ agentState: "shell" });
    render(Model);
    expect(screen.queryAllByRole("menuitemradio")).toHaveLength(0);
    expect(screen.getByText(copy.model.unavailable.title)).toBeInTheDocument();
    expect(screen.getByText(copy.model.unavailable.body)).toBeInTheDocument();
  });
});
