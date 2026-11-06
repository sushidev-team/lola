import { describe, it, expect, vi } from "vitest";
import { SSHBlock, SSHElement, SSHEvidence, Timeline, SSHTierTag, Tray, svc, ssh, store, SID, conn, block, el, seed, useSSHKit } from "./ssh.testkit";
import { inlineSpans, itemStatus, parseRich, richBlocks } from "$lib/ssh";
import { tick } from "svelte";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/svelte";

useSSHKit();

describe("no markup from agent or remote text", () => {
  it("nothing in the workspace uses {@html}", () => {
    const files = import.meta.glob("./*.svelte", { query: "?raw", import: "default", eager: true }) as Record<string, string>;
    expect(Object.keys(files).length).toBeGreaterThan(15);
    for (const [f, raw] of Object.entries(files)) {
      expect(raw.replace(/<!--[\s\S]*?-->/g, "").replace(/\/\/.*$/gm, ""), f).not.toContain("{@html");
    }
  });
});

describe("tier tag", () => {
  it("reads PRODUCTION for an unknown tier", () => {
    render(SSHTierTag, { tier: "mystery" });
    expect(screen.getByText("PRODUCTION")).toBeInTheDocument();
  });
});

describe("a block", () => {
  it("puts the why above the plate, because a plate holds only what lola witnessed", () => {
    seed([]);
    const { container } = render(SSHBlock, { sessionId: SID, block: block() as never });
    expect(container.querySelector("[data-plate] [data-claim='why']")).toBeNull();
    const why = container.querySelector("[data-claim='why']");
    expect(why?.textContent).toContain("see whether it restarted");
    const cmd = container.querySelector("[data-plate] [data-command-line]");
    expect(cmd?.textContent).toContain("systemctl status api");
    expect(why?.compareDocumentPosition(cmd as Node)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
  });
  it("leads with the command when there is no why", () => {
    seed([]);
    const { container } = render(SSHBlock, { sessionId: SID, block: block({ why: "" }) as never });
    expect(container.querySelector("[data-claim='why']")).toBeNull();
    expect(container.querySelector("[data-plate]")?.textContent).toContain("systemctl status api");
  });
  it("loses why in the audit view", () => {
    seed([]);
    render(SSHBlock, { sessionId: SID, block: block() as never, filter: "facts" });
    expect(screen.queryByText(/see whether it restarted/)).toBeNull();
  });
  it("says exit 3 and never failed", () => {
    seed([]);
    const { container } = render(SSHBlock, { sessionId: SID, block: block({ status: "failed", exitCode: 3, preview: "Active: failed" }) as never });
    expect(screen.getByText(/exit 3/)).toBeInTheDocument();
    const words = container.querySelector("[data-plate] .num")?.textContent ?? "";
    expect(words.toLowerCase()).not.toContain("failed");
  });
  it("shows an unknown status in words, with no tick", () => {
    seed([]);
    render(SSHBlock, { sessionId: SID, block: block({ status: "new_state", exitCode: undefined }) as never });
    expect(screen.getByText("new_state")).toBeInTheDocument();
  });
  it("expands in place, fetches output lazily and offers both digs", async () => {
    svc.Output.mockResolvedValue({ data: "disk is full\n", offset: 0, nextOffset: 13, totalBytes: 13, eof: true });
    seed([]);
    render(SSHBlock, { sessionId: SID, block: block({ stdoutBytes: 13 }) as never });
    expect(svc.Output).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByRole("button", { expanded: false }));
    await screen.findByText(/disk is full/);
    expect(svc.Output).toHaveBeenCalledWith(expect.objectContaining({ session: SID, block: "b1", stream: "stdout" }));
    await fireEvent.click(screen.getByRole("button", { name: "Ask Claude" }));
    expect(ssh.view(SID).agentDraft).toBe("About b1: ");
  });
  it("renders output as text, and links only http(s) through the daemon", async () => {
    const open = vi.spyOn(store, "openURL").mockResolvedValue();
    svc.Output.mockResolvedValue({ data: "<img src=x onerror=1> https://a.example javascript:x\n", offset: 0, nextOffset: 50, totalBytes: 50, eof: true });
    const v = seed([]);
    v.expanded.add("b1");
    const { container } = render(SSHBlock, { sessionId: SID, block: block({ stdoutBytes: 50 }) as never });
    const link = await screen.findByRole("link");
    expect(container.querySelector("img")).toBeNull();
    expect(link.textContent).toBe("https://a.example");
    await fireEvent.click(link);
    expect(open).toHaveBeenCalledWith("https://a.example");
    expect(screen.getAllByRole("link")).toHaveLength(1);
  });
  it("marks the dropped middle, and keeps the tail", async () => {
    svc.Output.mockResolvedValue({ data: "head\n", offset: 0, nextOffset: 5, totalBytes: 5, eof: true, droppedBytes: 3 * 1024 * 1024, tail: "the end\n" });
    const v = seed([]);
    v.expanded.add("b1");
    render(SSHBlock, { sessionId: SID, block: block({ stdoutBytes: 5 }) as never });
    await screen.findByText(/not kept/);
    expect(screen.getByText(/the end/)).toBeInTheDocument();
    expect(screen.getByText(/The middle is gone/)).toBeInTheDocument();
  });
  it("offers a retry when a fetch fails", async () => {
    svc.Output.mockRejectedValue(new Error("nope"));
    const v = seed([]);
    v.expanded.add("b1");
    render(SSHBlock, { sessionId: SID, block: block({ stdoutBytes: 5 }) as never });
    await screen.findByText("Could not load this output.");
    expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
  });
  it("stops with no question, and names what may survive", async () => {
    svc.Stop.mockResolvedValue({ block: block() });
    const v = seed([]);
    v.expanded.add("b1");
    render(SSHBlock, { sessionId: SID, block: block({ status: "running", exitCode: undefined }) as never });
    expect(screen.getByRole("button", { name: "Stop" }).getAttribute("title")).toMatch(/The remote process may keep running/);
    await fireEvent.click(screen.getByRole("button", { name: "Stop" }));
    expect(svc.Stop).toHaveBeenCalledWith("c1", "b1");
  });
});

describe("elements", () => {
  it("never draws a plate, for any kind", () => {
    seed([]);
    for (const kind of ["summary", "narration", "hypothesis", "finding", "plan", "metric", "evidence", "recommendation", "proposal", "question", "future_kind"]) {
      const { container, unmount } = render(SSHElement, { sessionId: SID, element: el({ kind, command: "x", steps: [], metrics: [] }) as never });
      expect(container.querySelector("[data-plate]"), kind).toBeNull();
      unmount();
    }
  });
  it("words a confirmed theory as the agent's, with no green", () => {
    seed([]);
    const { container } = render(SSHElement, { sessionId: SID, element: el({ kind: "hypothesis", state: "confirmed", title: "Disk full" }) as never });
    expect(screen.getByText("Agent says confirmed")).toBeInTheDocument();
    expect(container.innerHTML).not.toContain("text-good");
    expect(container.innerHTML).not.toContain("bg-good");
  });
  it("renders an unknown kind as plain text", () => {
    seed([]);
    render(SSHElement, { sessionId: SID, element: el({ kind: "from_the_future", title: "still readable", body: "and this" }) as never });
    expect(screen.getByText("still readable")).toBeInTheDocument();
    expect(screen.getByText("and this")).toBeInTheDocument();
  });
  it("renders text as text, never markup", () => {
    seed([]);
    const { container } = render(SSHElement, { sessionId: SID, element: el({ title: "<script>x</script><b>bold</b>" }) as never });
    expect(container.querySelector("b")).toBeNull();
    expect(container.textContent).toContain("<b>bold</b>");
  });
  it("turns a bare id into a plate only for a real block", () => {
    seed([{ id: "i", kind: "block", order: 1, seq: 1, block: block({ id: "b12" }) }]);
    const { container } = render(SSHElement, { sessionId: SID, element: el({ title: "see b12 and b99" }) as never });
    const shown = container.querySelectorAll("[data-evidence-command]");
    expect(shown).toHaveLength(1);
    expect(shown[0].closest("button")?.title).toContain(block({ id: "b12" }).command);
    expect(screen.queryByRole("button", { name: /b99/ })).toBeNull();
  });
  it("folds a finding's body behind More", async () => {
    seed([]);
    render(SSHElement, { sessionId: SID, element: el({ kind: "finding", severity: "high", title: "t", body: "the detail" }) as never });
    expect(screen.queryByText("the detail")).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: /More/ }));
    expect(screen.getByText("the detail")).toBeInTheDocument();
  });

  const proposal = (o: Record<string, unknown> = {}) =>
    el({ kind: "proposal", title: "Rotate logs", command: "logrotate -f /etc/x", state: "proposed", revision: 3, ...o });

  it("a proposal runs only through the check, with the drawn revision", async () => {
    vi.useFakeTimers();
    seed([]);
    render(SSHElement, { sessionId: SID, element: proposal() as never });
    expect(svc.Proposal).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByRole("button", { name: "Run on prod-api…" }));
    expect(svc.Proposal).not.toHaveBeenCalled();
    const run = screen.getByRole("button", { name: "Run on prod-api" });
    expect(run).toBeDisabled();
    await vi.advanceTimersByTimeAsync(800);
    expect(run).toBeEnabled();
    expect(screen.getByText("Run this yourself?")).toBeInTheDocument();
    await fireEvent.click(run);
    expect(svc.Proposal).toHaveBeenCalledWith(expect.objectContaining({ element: "e1", revision: 3, action: "run" }));
  });
  it("a moved revision closes the check and says so", async () => {
    seed([]);
    const r = render(SSHElement, { sessionId: SID, element: proposal() as never });
    await fireEvent.click(screen.getByRole("button", { name: "Run on prod-api…" }));
    await r.rerender({ sessionId: SID, element: proposal({ revision: 4, command: "rm -rf /" }) as never });
    await waitFor(() => expect(screen.queryByRole("button", { name: "Run on prod-api" })).toBeNull());
    expect(screen.getByRole("alert")).toHaveTextContent("Claude changed this proposal. Read it again.");
    expect(svc.Proposal).not.toHaveBeenCalled();
  });
  it("a server refusal for the same reason says the same sentence", async () => {
    vi.useFakeTimers();
    svc.Proposal.mockRejectedValue(new Error("the proposal has changed since you read it, so nothing ran"));
    seed([]);
    render(SSHElement, { sessionId: SID, element: proposal() as never });
    await fireEvent.click(screen.getByRole("button", { name: "Run on prod-api…" }));
    await vi.advanceTimersByTimeAsync(800);
    await fireEvent.click(screen.getByRole("button", { name: "Run on prod-api" }));
    await vi.advanceTimersByTimeAsync(0);
    expect(screen.getByRole("alert")).toHaveTextContent("Claude changed this proposal. Read it again.");
  });
  it("any other refusal is the daemon's sentence, not 'it changed'", async () => {
    vi.useFakeTimers();
    svc.Proposal.mockRejectedValue(new Error("the connection to prod-api is disconnected, so nothing ran"));
    const v = seed([]);
    render(SSHElement, { sessionId: SID, element: proposal() as never });
    await fireEvent.click(screen.getByRole("button", { name: "Run on prod-api…" }));
    await vi.advanceTimersByTimeAsync(800);
    await fireEvent.click(screen.getByRole("button", { name: "Run on prod-api" }));
    await vi.advanceTimersByTimeAsync(0);
    expect(screen.queryByRole("alert")).toBeNull();
    expect(v.flash).toContain("is disconnected, so nothing ran");
  });
  it("an answered question shows your words in your voice", () => {
    seed([]);
    render(SSHElement, { sessionId: SID, element: el({ kind: "question", title: "Go on?", state: "answered", outcome: { text: "Keep going" } }) as never });
    expect(screen.getByText("Keep going")).toBeInTheDocument();
    expect(screen.queryByRole("textbox")).toBeNull();
  });
  it("answers with the revision on screen", async () => {
    svc.Answer.mockResolvedValue({ item: "h", delivered: false });
    seed([]);
    render(SSHElement, { sessionId: SID, element: el({ kind: "question", title: "Go on?", state: "open", choices: ["Yes"], revision: 2 }) as never });
    await fireEvent.click(screen.getByRole("button", { name: "Yes" }));
    expect(svc.Answer).toHaveBeenCalledWith({ session: SID, element: "e1", revision: 2, text: "Yes" });
  });
});

describe("evidence plates", () => {
  it("never says verified", () => {
    seed([{ id: "i", kind: "block", order: 1, seq: 1, block: block() }]);
    const { container } = render(SSHEvidence, { sessionId: SID, block: "b1", verified: true });
    expect(container.textContent?.toLowerCase()).not.toContain("verified");
    expect(screen.getByRole("button").getAttribute("title")).toContain("That does not mean Claude's reading of it is correct");
  });
  it("reads not on record when the daemon could not find it, and is no link", () => {
    seed([]);
    render(SSHEvidence, { sessionId: SID, block: "b9", verified: false });
    expect(screen.getByText("b9 not on record")).toBeInTheDocument();
    expect(screen.queryByRole("button")).toBeNull();
  });
  it("digs: expands the block and asks the stream to land on it", async () => {
    const v = seed([{ id: "i", kind: "block", order: 1, seq: 1, block: block() }]);
    render(SSHEvidence, { sessionId: SID, block: "b1", verified: true });
    await fireEvent.click(screen.getByRole("button"));
    expect(v.expanded.has("b1")).toBe(true);
    expect(v.jump?.block).toBe("b1");
  });
});

describe("the stream", () => {
  it("leaves a key it does not use to the app, which ignores what a session has no use for", async () => {
    seed([{ id: "i", kind: "block", order: 1, seq: 1, block: block() }]);
    const onWindow = vi.fn();
    window.addEventListener("keydown", onWindow);
    render(Timeline, { sessionId: SID });
    await fireEvent.keyDown(screen.getByRole("log"), { key: "x" });
    expect(onWindow).toHaveBeenCalled();
    window.removeEventListener("keydown", onWindow);
  });
  it("leaves a chord alone", async () => {
    seed([]);
    const onWindow = vi.fn();
    window.addEventListener("keydown", onWindow);
    render(Timeline, { sessionId: SID });
    await fireEvent.keyDown(screen.getByRole("log"), { key: "c", metaKey: true });
    expect(onWindow).toHaveBeenCalled();
    window.removeEventListener("keydown", onWindow);
  });
  it("bare Y does nothing unless a card holds focus", async () => {
    vi.useFakeTimers();
    seed([{ id: "i", kind: "block", order: 1, seq: 1, block: block({ status: "pending_approval", approval: { granted: false } }) }]);
    render(Timeline, { sessionId: SID });
    render(Tray, { sessionId: SID });
    await vi.advanceTimersByTimeAsync(800);
    await fireEvent.keyDown(screen.getByRole("log"), { key: "Y" });
    await fireEvent.keyDown(window, { key: "Y" });
    expect(svc.Approve).not.toHaveBeenCalled();
  });
  it("walks the items and expands from the keyboard", async () => {
    const v = seed([
      { id: "i1", kind: "block", order: 1, seq: 1, block: block({ id: "b1" }) },
      { id: "i2", kind: "element", order: 2, seq: 2, element: el() },
    ]);
    render(Timeline, { sessionId: SID });
    const log = screen.getByRole("log");
    await fireEvent.keyDown(log, { key: "j" });
    expect(v.cursor).toBe("i1");
    await fireEvent.keyDown(log, { key: "Enter" });
    expect(v.expanded.has("b1")).toBe(true);
    await fireEvent.keyDown(log, { key: "ArrowLeft" });
    expect(v.expanded.has("b1")).toBe(false);
    await fireEvent.keyDown(log, { key: "a" });
    expect(v.agentDraft).toBe("About b1: ");
  });
  it("Facts hides every claim and folds nothing", async () => {
    const ok = (id: string, order: number) => ({ id, kind: "block", order, seq: order, block: block({ id: `b${order}`, command: `cmd${order}` }) });
    const v = seed([ok("a", 1), ok("b", 2), ok("c", 3), { id: "n", kind: "element", order: 4, seq: 4, element: el({ title: "a claim" }) }]);
    render(Timeline, { sessionId: SID });
    expect(screen.getByText(/all exit 0/)).toBeInTheDocument();
    expect(screen.getByText("a claim")).toBeInTheDocument();
    v.filter = "facts";
    await waitFor(() => expect(screen.queryByText("a claim")).toBeNull());
    expect(screen.getByText("cmd1")).toBeInTheDocument();
  });
  it("names the three voices in its legend", () => {
    seed([]);
    render(Timeline, { sessionId: SID });
  });
  it("words the empty states", async () => {
    const v = seed([]);
    render(Timeline, { sessionId: SID });
    expect(screen.getByText("Connected. Claude has not run anything yet.")).toBeInTheDocument();
    v.filter = "facts";
    await screen.findByText("No commands yet.");
    v.filter = "findings";
    await screen.findByText("Claude has not posted any findings or theories yet.");
  });
  it("offers a retry and a restart for the two fetch failures", async () => {
    const v = seed([]);
    v.error = "boom";
    const r = render(Timeline, { sessionId: SID });
    expect(screen.getByText("Could not load this timeline. Lola keeps recording it.")).toBeInTheDocument();
    r.unmount();
    v.predates = true;
    render(Timeline, { sessionId: SID });
    expect(screen.getByText("This daemon is older than this app. Restart it to see this conversation.")).toBeInTheDocument();
  });
  it("leaves a message that has not reached the agent to the queue rows, not the story", async () => {
    seed([{ id: "h1", kind: "human", order: 1, seq: 1, human: { text: "look there", delivered: false } }]);
    render(Timeline, { sessionId: SID });
    expect(screen.queryByText("look there")).toBeNull();
    expect(screen.queryByRole("button", { name: "Recall" })).toBeNull();
  });
});

describe("the agent's answer", () => {
  it("renders lists and code as structure, whole, and as text", () => {
    const blocks = richBlocks("3 containers:\n- **registry** on `5000`\n- db\n\n1. first\n```\n<b>x</b>\n```");
    expect(blocks.map((b) => b.kind)).toEqual(["lead", "ul", "ol", "code"]);
    expect(inlineSpans("**registry** on `5000`")).toEqual([
      { kind: "bold", text: "registry" },
      { kind: "text", text: " on " },
      { kind: "code", text: "5000" },
    ]);
    render(SSHElement, { sessionId: SID, element: { id: "e", kind: "narration", title: "Found:\n- <img src=x> one\n- two", revision: 1 } as never });
    expect(screen.getAllByRole("listitem")).toHaveLength(2);
    expect(document.querySelector("img")).toBeNull();
    expect(document.querySelector("[data-answer]")).not.toBeNull();
  });
  it("parses code inside bold, so a literal backtick never reaches the screen", () => {
    expect(inlineSpans("**\`#74\`:** derzeit")).toEqual([
      { kind: "bold", text: "\`#74\`:", spans: [{ kind: "code", text: "#74" }, { kind: "text", text: ":" }] },
      { kind: "text", text: " derzeit" },
    ]);
    expect(inlineSpans("**PR #74**")[0]).toMatchObject({ kind: "bold", spans: [{ kind: "text", text: "PR " }, { kind: "pr", text: "#74" }] });
    expect(inlineSpans("**a \`b**c\` d**")[0]).toMatchObject({ kind: "bold", text: "a \`b**c\` d" });
  });
  it("marks a pull request but not an image, a path, a word or a colour", () => {
    expect(inlineSpans("see #74.").map((s) => s.kind)).toEqual(["text", "pr", "text"]);
    for (const t of ["Image #4", "image #4", "Bild #4", "a/#12", "x#12", "#12ab", "#1234567", "##12", "\`#12\`"]) {
      expect(inlineSpans(t).some((s) => s.kind === "pr")).toBe(false);
    }
  });
  it("reads em with guards, and only an http link is a link", () => {
    expect(inlineSpans("an *important* one")[1]).toEqual({ kind: "em", text: "important" });
    for (const t of ["2*3*4", "a * b * c", "**", "* item"]) expect(inlineSpans(t).some((s) => s.kind === "em")).toBe(false);
    expect(inlineSpans("[docs](https://x.dev/a)")).toEqual([{ kind: "link", text: "docs", href: "https://x.dev/a" }]);
    expect(inlineSpans("[bad](javascript:alert(1))")).toEqual([{ kind: "text", text: "[bad](javascript:alert(1))" }]);
    expect(inlineSpans("[bad](file:///etc/passwd)")).toEqual([{ kind: "text", text: "[bad](file:///etc/passwd)" }]);
  });
  it("leaves unbalanced markers literal, and does not freeze on them", () => {
    expect(inlineSpans("**open and \`code\`")).toEqual([{ kind: "text", text: "**open and " }, { kind: "code", text: "code" }]);
    const line = "**" + "\`x\` a ".repeat(400);
    const started = performance.now();
    expect(inlineSpans(line).length).toBeGreaterThan(100);
    expect(performance.now() - started).toBeLessThan(500);
  });
  it("hangs an indented item on the one above it, so a numbered list keeps its count", () => {
    expect(parseRich(["1. first", "   - child", "2. second"])).toEqual([{ kind: "ol", items: ["first", "second"], subs: { 0: ["child"] } }]);
    expect(richBlocks("- a\n- b")[0]).toEqual({ kind: "ul", items: ["a", "b"] });
    render(SSHElement, { sessionId: SID, element: { id: "e", kind: "narration", title: "Found:\n- top\n  - nested one", revision: 1 } as never });
    expect(screen.getByText("nested one")).toBeInTheDocument();
  });
  it("keeps a one-line status quiet", () => {
    render(SSHElement, { sessionId: SID, element: { id: "e", kind: "narration", title: "Reading the journal", revision: 1 } as never });
    expect(document.querySelector("[data-answer]")).toBeNull();
  });
});

describe("answers with structure", () => {
  it("parses tables, callouts, folds, marks and a headline", () => {
    const b = richBlocks("GitLab is healthy\n| Container | x |\n|---|---|\n| Image | `y` |\n- [ok] docker\n> [!WARN] disk at 90%\n+++ Ports\n- 443\n+++");
    expect(b.map((x) => x.kind)).toEqual(["lead", "table", "ul", "callout", "fold"]);
    expect(b[1]).toMatchObject({ head: ["Container", "x"], rows: [["Image", "`y`"]] });
    expect(b[3]).toMatchObject({ tone: "warn", text: "disk at 90%" });
    expect(b[4]).toMatchObject({ title: "Ports" });
    expect(itemStatus("[ok] docker")).toEqual({ tone: "ok", text: "docker" });
    expect(itemStatus("[x] not a mark").tone).toBeUndefined();
  });
  it("never eats text it does not understand", () => {
    expect(richBlocks("a | b, and no rule under it")[0]).toMatchObject({ kind: "p" });
    expect(richBlocks("+++ never closed\nstill here")[0]).toMatchObject({ kind: "fold" });
    expect(JSON.stringify(richBlocks("> [!NOPE] kept"))).toContain("[!NOPE] kept");
  });
  it("renders it all as text, with the fold closed", () => {
    render(SSHElement, { sessionId: SID, element: { id: "e", kind: "narration", title: "Done\n| a | b |\n|---|---|\n| <img src=x> | 1 |\n+++ More\nsecret\n+++", revision: 1 } as never });
    expect(document.querySelector("[data-rich-lead]")?.textContent).toBe("Done");
    expect(document.querySelector("img")).toBeNull();
    expect(document.querySelector("[data-rich-table]")?.textContent).toContain("<img src=x>");
    expect(screen.getByText("secret").closest("[hidden]")).not.toBeNull();
  });
});

describe("the conversation", () => {
  it("shows no empty message, and keeps one with a transcript", () => {
    seed([
      { id: "h1", kind: "human", order: 1, seq: 1, at: "", human: { origin: "handback", text: "", delivered: true } },
      { id: "h2", kind: "human", order: 2, seq: 2, at: "", human: { origin: "say", text: "hello", delivered: true } },
      { id: "h3", kind: "human", order: 3, seq: 3, at: "", human: { origin: "handback", text: "", transcript: "ls", delivered: true } },
    ] as never);
    render(Timeline, { sessionId: SID });
    expect(document.querySelector('[data-row="h1"]')).toBeNull();
    expect(document.querySelector('[data-row="h2"]')).not.toBeNull();
    expect(document.querySelector('[data-row="h3"]')).not.toBeNull();
  });
  it("draws a plate the same whoever ran it, and names your own", () => {
    seed([]);
    const { container } = render(SSHBlock, { sessionId: SID, block: block({ actor: "human" }) as never });
    expect(container.querySelector("[data-block]")?.getAttribute("data-side")).toBeNull();
    expect(container.querySelector("[data-plate]")?.className).not.toContain("accent");
    expect(container.querySelector("[data-command-line]")?.textContent).toContain("You");
    cleanup();
    render(SSHElement, { sessionId: SID, element: { id: "e", kind: "narration", title: "Done\n- a", revision: 1 } as never });
    expect(document.querySelector("[data-answer]")?.className).not.toContain("border");
  });
});

describe("one open command", () => {
  it("closes the open one when another opens, whoever ran it", async () => {
    const v = seed([]);
    v.expanded.add("b1");
    render(SSHBlock, { sessionId: SID, block: block({ id: "b2", actor: "human" }) as never });
    await fireEvent.click(screen.getByRole("button", { expanded: false }));
    expect([...v.expanded]).toEqual(["b2"]);
  });
  it("closes it when your command opens by itself", async () => {
    svc.RunCommand.mockReset().mockResolvedValue({ id: "b9" });
    const v = seed([], [conn({ tier: "dev", state: "connected" })]);
    v.expanded.add("b1");
    await ssh.runCommand(SID, "c1", "uptime");
    expect([...v.expanded]).toEqual(["b9"]);
  });
});

describe("an answer that arrived as a blob", () => {
  const blob = "GitLab on cp-test-01 is healthy. test-sushi-gitlab, 17.11.1-ce: Up 8 weeks (healthy), 0 restarts. https://localhost/-/readiness returns 200. Disk: /var/opt/gitlab 19%, /var 22%. Not checked: the docker log scan timed out, so recent errors are unknown.";
  it("becomes a headline and a list, keeping every word in order", () => {
    const b = richBlocks(blob);
    expect(b.map((x) => x.kind)).toEqual(["lead", "ul"]);
    expect(b[0]).toEqual({ kind: "lead", text: "GitLab on cp-test-01 is healthy." });
    const items = (b[1] as { items: string[] }).items;
    expect(items).toContain("**Disk:** /var/opt/gitlab 19%, /var 22%.");
    expect(items.some((i) => i.startsWith("**Not checked:**"))).toBe(true);
    const words = (x: string) => x.replace(/\*\*/g, "").split(/\s+/);
    expect([(b[0] as { text: string }).text, ...items].flatMap(words)).toEqual(words(blob));
  });
  it("leaves a version number, a short answer and a structured one alone", () => {
    expect(JSON.stringify(richBlocks(blob))).toContain("17.11.1-ce");
    expect(richBlocks("Healthy. All fine.")).toEqual([{ kind: "p", text: "Healthy. All fine." }]);
    expect(richBlocks("x".repeat(300))[0]).toMatchObject({ kind: "p" });
    expect(richBlocks("Head\n- a")[0]).toMatchObject({ kind: "lead" });
  });
});
