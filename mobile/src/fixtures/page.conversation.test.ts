import { afterEach, describe, expect, it, vi } from "vitest";
import { waitFor, within } from "@testing-library/svelte";
import { openPreview, PAGE_TIMEOUT } from "./page.helpers";

const errors = vi.spyOn(console, "error");

afterEach(() => errors.mockClear());

describe("preview page: conversation", () => {
  it("renders the pushed story on the Conversation segment without an effect loop", async () => {
    const app = await openPreview("?fixture=long-story&route=/session/s3/conversation");
    await waitFor(() => expect(app.querySelector('[data-fixture="long-story"]')).not.toBeNull(), { timeout: PAGE_TIMEOUT });
    await waitFor(() => expect(app.textContent).toContain("Extract the invoice presenter"), { timeout: PAGE_TIMEOUT });
    await waitFor(() => expect(app.textContent).toContain("tighten the accounting period view"), { timeout: PAGE_TIMEOUT });
    // The segment installs the attach picker before its composer renders.
    await waitFor(() => expect(app.querySelector('button[aria-label="Attach image"]')).not.toBeNull(), { timeout: PAGE_TIMEOUT });
    const loops = errors.mock.calls.flat().filter((a) => String(a).includes("effect_update_depth_exceeded"));
    expect(loops).toEqual([]);
  }, PAGE_TIMEOUT * 2);
});

describe("preview page: composer fixture", () => {
  it("renders the attach sheet over the populated conversation", async () => {
    // One page per module registry: the stores and the router are singletons.
    vi.resetModules();
    const app = await openPreview("?fixture=composer&route=/session/s3/conversation%3Fsheet%3Dattach");
    await waitFor(() => expect(app.querySelector('[role="dialog"]')).not.toBeNull(), { timeout: PAGE_TIMEOUT });
    expect(within(app).getByRole("dialog", { name: "Add image" })).toBeTruthy();
    // The sheet is a layer over the segment, so the story is still there.
    await waitFor(() => expect(app.textContent).toContain("tighten the accounting period view"), { timeout: PAGE_TIMEOUT });
    expect(app.querySelector("[data-conversation]")).not.toBeNull();
    expect(app.querySelector("[data-composer]")).not.toBeNull();
  }, PAGE_TIMEOUT * 2);
});

describe("preview page: composer fixture with the keyboard up", () => {
  it("holds a multi-line draft and a collapsed two-message queue", async () => {
    vi.resetModules();
    const app = await openPreview("?fixture=composer&route=/session/s3/conversation&kb=336");
    await waitFor(() => expect(app.querySelector("[data-queue-phone]")).not.toBeNull(), { timeout: PAGE_TIMEOUT });
    const tray = app.querySelector("[data-queue-phone]")!;
    expect(tray.textContent).toMatch(/Queued · 2/i);
    expect(tray.querySelector("[data-queue-toggle]")?.getAttribute("aria-expanded")).toBe("false");
    expect(tray.querySelector("[data-queue-row]")).toBeNull();
    const field = app.querySelector<HTMLTextAreaElement>("[data-composer-field] textarea");
    expect(field?.value.split("\n")).toHaveLength(6);
  }, PAGE_TIMEOUT * 2);
});

describe("preview page: dialog fixture", () => {
  it("shows the modal card with Open terminal and no inline terminal", async () => {
    vi.resetModules();
    const app = await openPreview("?fixture=dialog&route=/session/s3/conversation");
    await waitFor(() => expect(app.querySelector("[data-card=modal]")).not.toBeNull(), { timeout: PAGE_TIMEOUT });
    expect(within(app).getByRole("button", { name: "Open terminal" })).toBeTruthy();
    // The inline terminal cell stays in the DOM but is never drawn: its name is empty on a phone.
    const cell = app.querySelector<HTMLElement>("[data-inline-terminal]");
    expect(cell === null || cell.hidden).toBe(true);
  }, PAGE_TIMEOUT * 2);
});
