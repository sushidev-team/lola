import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/svelte";
import { describe, it, expect, afterEach, vi } from "vitest";
import Toast from "./Toast.svelte";
import { store } from "$lib/store.svelte";

describe("Toast", () => {
  it("offers the recovery action and dismisses the toast before running it", async () => {
    render(Toast);
    const run=vi.fn(()=>expect(store.flash).toBeNull());
    store.setFlash('Push blocked','warn',{description:'Pull changes, then push again.',action:{label:'Pull changes',run}});
    expect(await screen.findByText('Pull changes, then push again.')).toBeInTheDocument();
    await fireEvent.click(screen.getByRole('button',{name:'Pull changes'}));
    expect(run).toHaveBeenCalledOnce();
  });
  afterEach(() => {
    store.dismissFlash();
    cleanup();
  });

  it("shows a flash in sentence case and dismisses it", async () => {
    render(Toast);
    store.setFlash("daemon restarted");
    expect(await screen.findByText("Daemon restarted")).toBeInTheDocument();
    await fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(store.flash).toBeNull();
    // The outro runs, then the toast leaves the DOM.
    await waitFor(() => expect(screen.queryByText("Daemon restarted")).not.toBeInTheDocument());
  });

  it("keeps a leading identifier verbatim", async () => {
    render(Toast);
    store.setFlash("eng-123 killed", "bad");
    expect(await screen.findByText("eng-123 killed")).toBeInTheDocument();
  });
});
