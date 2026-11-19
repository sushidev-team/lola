import { render, screen, fireEvent, cleanup } from "@testing-library/svelte";
import { describe, it, expect, afterEach } from "vitest";
import { flushSync } from "svelte";
import StackAppsDialog from "./StackAppsDialog.svelte";
import { appChooser, type AppChooserRequest } from "$lib/appchooser.svelte";

const base: AppChooserRequest = {
  title: "Repositories for NOR-2",
  intro: "Tick the ones it needs.",
  confirm: "Start",
  requireOne: false,
  choices: [
    { name: "web", label: "Web", included: false, note: "" },
    { name: "docs", label: "Docs", included: true, note: "every issue" },
  ],
};

describe("StackAppsDialog", () => {
  afterEach(() => {
    cleanup();
    appChooser.finish(null);
  });

  it("answers with the repositories the person ticked", async () => {
    render(StackAppsDialog);
    const answer = appChooser.ask(base);
    flushSync();
    expect(screen.getByRole("dialog", { name: "Repositories for NOR-2" })).toBeInTheDocument();
    const [web, docs] = screen.getAllByRole("checkbox") as HTMLInputElement[];
    // What the session already gets is shown, ticked and fixed.
    expect(docs.checked && docs.disabled).toBe(true);
    await fireEvent.click(web);
    await fireEvent.click(screen.getByRole("button", { name: "Start" }));
    expect(await answer).toEqual(["web"]);
    expect(appChooser.request).toBeNull();
  });

  it("lets a start go ahead with what is already included, and answers null on cancel", async () => {
    render(StackAppsDialog);
    const first = appChooser.ask(base);
    flushSync();
    await fireEvent.click(screen.getByRole("button", { name: "Start" }));
    expect(await first).toEqual([]);

    const second = appChooser.ask(base);
    flushSync();
    await fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(await second).toBeNull();
  });

  it("needs a choice when nothing would be checked out, or when adding", () => {
    render(StackAppsDialog);
    void appChooser.ask({ ...base, choices: [base.choices[0]] });
    flushSync();
    expect(screen.getByRole("button", { name: "Start" })).toBeDisabled();
    appChooser.finish(null);

    void appChooser.ask({ ...base, confirm: "Add", requireOne: true });
    flushSync();
    expect(screen.getByRole("button", { name: "Add" })).toBeDisabled();
  });
});
