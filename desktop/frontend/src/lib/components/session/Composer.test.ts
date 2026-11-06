import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { tick } from "svelte";
import { conn, freshSession, setTalk, ssh, sshSvc, store, talkSvc, terms, AGENT } from "./talk.testkit";
import { attachmentToken } from "$lib/composerpaste";
import Composer from "./Composer.svelte";
import Tray from "./Tray.svelte";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.clearAllMocks();
});

const field = () => screen.getByRole("textbox") as HTMLTextAreaElement;
const type = (v: string) => fireEvent.input(field(), { target: { value: v } });
const shown = (id: string) => render(Composer, { sessionId: id });
const button = (name: RegExp | string) => screen.queryByRole("button", { name });
const left = () => document.querySelector("[data-footer-left]") as HTMLElement;

describe("no host", () => {
  it("no host means no Run, no ⌘ preview, no swap", async () => {
    const id = freshSession();
    shown(id);
    await setTalk(id);
    expect(button(/Run on/)).toBeNull();
    await type("hello");
    await fireEvent.keyDown(field(), { key: "Meta" });
    await new Promise((r) => setTimeout(r, 220));
    expect(document.querySelector('[data-composer="run"]')).toBeNull();
    await fireEvent.keyUp(field(), { key: "Meta" });
    // A double tap.
    for (let i = 0; i < 2; i++) {
      await fireEvent.keyDown(field(), { key: "Meta" });
      await fireEvent.keyUp(field(), { key: "Meta" });
    }
    expect(ssh.view(id).runDefault).toBe(false);
    expect(document.querySelector('[data-composer="ask"]')).not.toBeNull();
  });

  it("⌘↩ with no host keeps the draft and says why", async () => {
    const id = freshSession();
    shown(id);
    await type("ls -la");
    await fireEvent.keyDown(field(), { key: "Enter", metaKey: true });
    expect(ssh.view(id).agentDraft).toBe("ls -la");
    expect(sshSvc.RunCommand).not.toHaveBeenCalled();
    expect(talkSvc.Say).not.toHaveBeenCalled();
    expect(screen.getByRole("status").textContent).toBe("Run needs a host. ⌘T opens a shell.");
  });
});

describe("keys", () => {
  it("⌥↩ inserts a newline", async () => {
    const id = freshSession();
    shown(id);
    await type("one");
    field().setSelectionRange(3, 3);
    await fireEvent.keyDown(field(), { key: "Enter", altKey: true });
    expect(ssh.view(id).agentDraft).toBe("one\n");
    expect(talkSvc.Say).not.toHaveBeenCalled();
  });

  it("⇧↩ leaves the newline to the field and sends nothing", async () => {
    const id = freshSession();
    shown(id);
    await type("one");
    await fireEvent.keyDown(field(), { key: "Enter", shiftKey: true });
    expect(talkSvc.Say).not.toHaveBeenCalled();
  });

  it("↩ sends through agentSay and clears the field", async () => {
    const id = freshSession();
    shown(id);
    await setTalk(id);
    await type("carry on");
    await fireEvent.keyDown(field(), { key: "Enter" });
    await waitFor(() => expect(talkSvc.Say).toHaveBeenCalledWith({ session: id, text: "carry on", conn: undefined, stopFirst: undefined }));
    await waitFor(() => expect(ssh.view(id).agentDraft).toBe(""));
  });

  it("Esc blurs and keeps the draft", async () => {
    const id = freshSession();
    shown(id);
    await type("half");
    field().focus();
    await fireEvent.keyDown(field(), { key: "Escape" });
    expect(document.activeElement).not.toBe(field());
    expect(ssh.view(id).agentDraft).toBe("half");
  });

  it("falls back to ssh.say on unknown cmd", async () => {
    const id = freshSession();
    talkSvc.Say.mockRejectedValue(new Error('unknown cmd "agentSay"'));
    sshSvc.Say.mockResolvedValue({});
    shown(id);
    await type("old daemon");
    await fireEvent.keyDown(field(), { key: "Enter" });
    await waitFor(() => expect(sshSvc.Say).toHaveBeenCalledWith(id, "", "old daemon"));
    await waitFor(() => expect(ssh.view(id).agentDraft).toBe(""));
  });

  it("keeps the draft when the daemon refuses", async () => {
    const id = freshSession();
    talkSvc.Say.mockRejectedValue(new Error("A goal runs this session."));
    shown(id);
    await type("still here");
    await fireEvent.keyDown(field(), { key: "Enter" });
    await waitFor(() => expect(screen.getByRole("status").textContent).toContain("A goal runs this session."));
    expect(ssh.view(id).agentDraft).toBe("still here");
  });

  it("↑ on an empty field takes the newest queued message back, then walks history", async () => {
    const id = freshSession();
    const v = ssh.view(id);
    v.items = [{ id: "q1", human: { text: "first", delivered: false } }, { id: "q2", human: { text: "second", delivered: false } }] as never;
    v.pushHistory("agent", "older");
    shown(id);
    await fireEvent.keyDown(field(), { key: "ArrowUp" });
    await waitFor(() => expect(v.agentDraft).toBe("second"));
    expect(sshSvc.Recall).toHaveBeenCalledWith(id, "q2");
    v.agentDraft = "";
    v.items = [] as never;
    await fireEvent.keyDown(field(), { key: "ArrowUp" });
    await waitFor(() => expect(v.agentDraft).toBe("older"));
  });

  it("a recall that came too late keeps nothing in the field", async () => {
    const id = freshSession();
    const v = ssh.view(id);
    v.items = [{ id: "q1", human: { text: "gone already", delivered: false } }] as never;
    sshSvc.Recall.mockResolvedValue({ recalled: false });
    shown(id);
    await fireEvent.keyDown(field(), { key: "ArrowUp" });
    await waitFor(() => expect(sshSvc.Recall).toHaveBeenCalled());
    await waitFor(() => expect(v.flash).toContain("Too late"));
    expect(v.agentDraft).toBe("");
  });
});

describe("hosts", () => {
  it("Run names its host", async () => {
    const id = freshSession();
    const v = ssh.view(id);
    v.connections = [conn("prod-api", { tier: "production" })];
    shown(id);
    await setTalk(id);
    expect(button(/Run on prod-api/)).not.toBeNull();
    await type("uptime");
    await fireEvent.keyDown(field(), { key: "Enter", metaKey: true });
    await waitFor(() => expect(sshSvc.RunCommand).toHaveBeenCalledWith({ session: id, conn: "prod-api", command: "uptime" }));
    await waitFor(() => expect(v.agentDraft).toBe(""));
  });

  it("All with two hosts offers no Run", async () => {
    const id = freshSession();
    const v = ssh.view(id);
    v.connections = [conn("prod-api", { tier: "production" }), conn("staging", { tier: "staging" })];
    shown(id);
    await setTalk(id);
    expect(button(/Run on/)).toBeNull();
    expect(document.querySelector("[data-footer-hint]")?.textContent).toBe("Pick a host above to run");
    await type("uptime");
    await fireEvent.keyDown(field(), { key: "Enter", metaKey: true });
    expect(sshSvc.RunCommand).not.toHaveBeenCalled();
    expect(v.agentDraft).toBe("uptime");
    expect(screen.getByRole("status").textContent).toBe("Pick a host above to run.");
  });

  it("draft survives a scope change", async () => {
    const id = freshSession();
    const v = ssh.view(id);
    v.connections = [conn("a"), conn("b")];
    shown(id);
    await type("about both hosts");
    v.scope = "a";
    await waitFor(() => expect(button(/Run on a/)).not.toBeNull());
    expect(field().value).toBe("about both hosts");
    v.scope = "";
    await waitFor(() => expect(button(/Run on a/)).toBeNull());
    expect(field().value).toBe("about both hosts");
  });

  it("history does not cross hosts", async () => {
    const id = freshSession();
    const v = ssh.view(id);
    v.connections = [conn("staging"), conn("prod")];
    v.scope = "staging";
    v.runDefault = true;
    v.pushHistory("staging", "reboot");
    shown(id);
    await fireEvent.keyDown(field(), { key: "ArrowUp" });
    await waitFor(() => expect(field().value).toBe("reboot"));
    await fireEvent.keyDown(field(), { key: "ArrowDown" });
    await waitFor(() => expect(field().value).toBe(""));
    v.scope = "prod";
    await waitFor(() => expect(button(/Run on prod/)).not.toBeNull());
    await fireEvent.keyDown(field(), { key: "ArrowUp" });
    expect(field().value).toBe("");
    expect(v.commandDraft("prod")).toBe("");
  });

  it("a swap keeps a command's draft apart from the agent's", async () => {
    const id = freshSession();
    const v = ssh.view(id);
    v.connections = [conn("a")];
    v.agentDraft = "a sentence";
    v.setCommandDraft("a", "uptime");
    v.runDefault = true;
    shown(id);
    expect(field().value).toBe("uptime");
    expect(document.querySelector('[data-composer="run"]')).not.toBeNull();
    v.runDefault = false;
    await waitFor(() => expect(field().value).toBe("a sentence"));
    expect(document.querySelector('[data-composer="ask"]')).not.toBeNull();
  });

  it("speaks with the host in scope, and with none under All", async () => {
    const id = freshSession();
    const v = ssh.view(id);
    v.connections = [conn("a", { wheel: "human" })];
    shown(id);
    await setTalk(id);
    await type("under all");
    await fireEvent.keyDown(field(), { key: "Enter" });
    await waitFor(() => expect(talkSvc.Say).toHaveBeenCalledWith(expect.objectContaining({ conn: undefined })));
    expect(v.connections[0].wheel).toBe("human");
    v.scope = "a";
    await waitFor(() => expect(v.agentDraft).toBe(""));
    await type("with one");
    await fireEvent.keyDown(field(), { key: "Enter" });
    await waitFor(() => expect(talkSvc.Say).toHaveBeenLastCalledWith(expect.objectContaining({ conn: "a" })));
    await waitFor(() => expect(v.connections[0].wheel).toBe("agent"));
  });

  it("says who is driving and offers the way back", async () => {
    const id = freshSession();
    const v = ssh.view(id);
    v.connections = [conn("prod-api", { wheel: "human" })];
    sshSvc.HandbackPreview.mockResolvedValue({ transcript: "" });
    shown(id);
    await setTalk(id);
    expect(left().textContent).toContain("You're driving prod-api");
    await fireEvent.click(button("Hand back")!);
    expect(v.handback?.conn).toBe("prod-api");
  });
});

describe("stop", () => {
  it("stop keeps position when text appears", async () => {
    const id = freshSession({ agentState: "working" });
    shown(id);
    await setTalk(id, { composer: "working" });
    const stopBefore = left().querySelector("[data-footer-stop]");
    expect(stopBefore).not.toBeNull();
    const before = [...left().children].indexOf(stopBefore!);
    await type("more");
    expect(left().querySelector("[data-footer-stop]")).toBe(stopBefore);
    expect([...left().children].indexOf(stopBefore!)).toBe(before);
    expect(button("Stop & send")).not.toBeNull();
    // Stop & send sits in another group, so its arrival cannot push Stop.
    expect(left().contains(button("Stop & send"))).toBe(false);
  });

  it("stop refuses clicks for 800ms after a send", async () => {
    const id = freshSession({ agentState: "working" });
    shown(id);
    await setTalk(id, { composer: "working" });
    await type("queued");
    await fireEvent.keyDown(field(), { key: "Enter" });
    await waitFor(() => expect(talkSvc.Say).toHaveBeenCalled());
    await waitFor(() => expect(ssh.view(id).agentDraft).toBe(""));
    await fireEvent.click(document.querySelector("[data-footer-stop]")!);
    expect(talkSvc.Stop).not.toHaveBeenCalled();
    await new Promise((r) => setTimeout(r, 820));
    await fireEvent.click(document.querySelector("[data-footer-stop]")!);
    expect(talkSvc.Stop).toHaveBeenCalledTimes(1);
  });

  it("Stop & send carries the text and asks to stop first", async () => {
    const id = freshSession({ agentState: "working" });
    shown(id);
    await setTalk(id, { composer: "working" });
    await type("instead do this");
    await fireEvent.click(button("Stop & send")!);
    await waitFor(() => expect(talkSvc.Say).toHaveBeenCalledWith({ session: id, text: "instead do this", conn: undefined, stopFirst: true }));
  });

  it("Revive also refuses clicks right after a send", async () => {
    const id = freshSession({ agentState: "dead" });
    const revive = vi.spyOn(store, "revive").mockResolvedValue(undefined);
    shown(id);
    await setTalk(id, { composer: "gone" });
    await type("for later");
    await fireEvent.keyDown(field(), { key: "Enter" });
    await waitFor(() => expect(talkSvc.Say).toHaveBeenCalled());
    await waitFor(() => expect(ssh.view(id).agentDraft).toBe(""));
    await fireEvent.click(document.querySelector('[data-footer-primary="revive"]')!);
    expect(revive).not.toHaveBeenCalled();
    revive.mockRestore();
  });

  it("a stop the daemon refuses says why in the footer", async () => {
    const id = freshSession({ agentState: "working" });
    talkSvc.Stop.mockResolvedValue({ stopped: false, why: "Already stopped" });
    shown(id);
    await setTalk(id, { composer: "working" });
    await fireEvent.click(document.querySelector("[data-footer-stop]")!);
    await waitFor(() => expect(screen.getByRole("status").textContent).toBe("Already stopped"));
  });
});

describe("what the field holds", () => {
  it("widget fill shows the chip and rail", async () => {
    const id = freshSession();
    const v = ssh.view(id);
    shown(id);
    v.fillFromWidget("Queue depth", "drain the queue");
    await waitFor(() => expect(document.querySelector("[data-fill-chip]")?.textContent).toContain("Filled by Queue depth"));
    expect(document.querySelector("[data-fill-rail]")).not.toBeNull();
    await fireEvent.click(button("Clear")!);
    expect(v.agentDraft).toBe("");
    await waitFor(() => expect(document.querySelector("[data-fill-chip]")).toBeNull());
  });

  it("a slash prefix fills and never sends", async () => {
    const id = freshSession();
    shown(id);
    await setTalk(id);
    await type("/comp");
    expect(document.querySelector("[data-slash-list]")).not.toBeNull();
    await fireEvent.keyDown(field(), { key: "Enter" });
    expect(talkSvc.Say).not.toHaveBeenCalled();
    expect(ssh.view(id).agentDraft).toBe("/compact");
  });

  it("a screen command opens the agent's terminal with nothing typed", async () => {
    const id = freshSession();
    const select = vi.spyOn(terms, "select");
    shown(id);
    await setTalk(id);
    await type("/config");
    expect(screen.getByText("Opens in terminal")).toBeTruthy();
    await fireEvent.keyDown(field(), { key: "Enter" });
    expect(talkSvc.Say).not.toHaveBeenCalled();
    expect(select).toHaveBeenCalledWith(id, AGENT);
    select.mockRestore();
  });

  it("an unknown word warns, and the second ↩ sends", async () => {
    const id = freshSession();
    shown(id);
    await setTalk(id);
    await type("/nonsense do it");
    await fireEvent.keyDown(field(), { key: "Enter" });
    expect(talkSvc.Say).not.toHaveBeenCalled();
    await waitFor(() => expect(document.querySelector("[data-composer-note]")?.textContent).toBe("lola doesn't know /nonsense. Claude drops the text if it doesn't either."));
    await fireEvent.keyDown(field(), { key: "Enter" });
    await waitFor(() => expect(talkSvc.Say).toHaveBeenCalled());
  });

  it("says what a leading ! becomes", async () => {
    const id = freshSession();
    shown(id);
    await type("!ls");
    expect(document.querySelector("[data-composer-note]")?.textContent).toBe("Sent as text. To run on this machine, use the terminal.");
  });

  it("shows the counter from 75% of the cap, and refuses past it", async () => {
    const id = freshSession();
    shown(id);
    await setTalk(id);
    await type("x".repeat(90_000));
    expect(document.querySelector("[data-composer-counter]")).toBeNull();
    await type("x".repeat(100_000));
    expect(document.querySelector("[data-composer-counter]")).not.toBeNull();
    await type("x".repeat(140_000));
    await fireEvent.keyDown(field(), { key: "Enter" });
    expect(talkSvc.Say).not.toHaveBeenCalled();
    expect((document.querySelector("[data-footer-primary]") as HTMLButtonElement).disabled).toBe(true);
  });

  it("says the daemon is down, keeps the draft, and sends nothing", async () => {
    const id = freshSession();
    const v = ssh.view(id);
    v.agentDraft = "kept";
    store.alive = false;
    shown(id);
    expect(left().textContent).toContain("Daemon down. Your draft is kept and nothing is queued.");
    expect(field().value).toBe("kept");
    expect((document.querySelector("[data-footer-primary]") as HTMLButtonElement).disabled).toBe(true);
    await fireEvent.keyDown(field(), { key: "Enter" });
    expect(talkSvc.Say).not.toHaveBeenCalled();
    expect(v.agentDraft).toBe("kept");
    store.alive = true;
  });
});

describe("reaching a card", () => {
  it("Tab from an empty field reaches the card", async () => {
    const id = freshSession();
    render(Tray, { sessionId: id });
    shown(id);
    await setTalk(id, { composer: "prompt", prompt: { kind: "modal", hash: "h", lines: ["a screen"] } });
    await waitFor(() => expect(document.querySelector('[data-card="modal"]')).not.toBeNull());
    field().focus();
    await fireEvent.keyDown(field(), { key: "Tab" });
    await waitFor(() => expect(document.activeElement).toBe(document.querySelector('[data-card="modal"]')));
  });

  it("Tab with text leaves focus alone", async () => {
    const id = freshSession();
    const v = ssh.view(id);
    render(Tray, { sessionId: id });
    shown(id);
    await setTalk(id, { composer: "prompt", prompt: { kind: "modal", hash: "h", lines: ["a screen"] } });
    await type("hello");
    const before = v.dockFocus;
    await fireEvent.keyDown(field(), { key: "Tab" });
    expect(v.dockFocus).toBe(before);
  });
});

describe("attachments in the field", () => {
  const paste = (text: string) => fireEvent.paste(field(), { clipboardData: { getData: () => text } });

  it("a long paste becomes a numbered tile and a tinted number, and goes as its text", async () => {
    const id = freshSession();
    shown(id);
    await setTalk(id);
    const log = "line\n".repeat(10);
    await paste(log);
    await tick();
    expect(document.querySelector("[data-attachment=text]")).not.toBeNull();
    const ref = document.querySelector("[data-field-ref='1']");
    expect(ref?.className).toContain("bg-accent/20");
    expect(field().className).toContain("text-transparent");
    await fireEvent.keyDown(field(), { key: "Enter" });
    await tick();
    expect(talkSvc.Say).toHaveBeenCalledWith(expect.objectContaining({ text: log.trimEnd() }));
  });

  it("the same paste again straight away shows it as text, and a plain field has no layer", async () => {
    const id = freshSession();
    shown(id);
    await setTalk(id);
    const log = "line\n".repeat(10);
    await paste(log);
    await tick();
    await paste(log);
    await tick();
    expect(field().value).toBe(log);
    expect(document.querySelector("[data-attachment]")).toBeNull();
    expect(document.querySelector("[data-field-mirror]")).toBeNull();
    expect(field().className).toContain("text-ink");
  });

  it("a tile with no thumbnail shows a glyph and no word, and its title still says what it is", async () => {
    const id = freshSession();
    const view = ssh.view(id);
    view.pastes.set(1, { kind: "image", value: "/w/.lola/images/a.png" });
    view.agentDraft = attachmentToken(1);
    shown(id);
    await setTalk(id);
    await tick();
    const tile = document.querySelector("[data-attachment=image]") as HTMLElement;
    expect(tile.querySelector("[data-attach-glyph=image]")).not.toBeNull();
    expect(tile.querySelector("[title=Image]")).not.toBeNull();
    expect(tile.textContent).not.toContain("Image");
  });
});

describe('send feedback', () => {
  it('clears the field and shows loading immediately, preserving a new draft', async () => {
    const id = freshSession({ agentState: 'working' });
    let resolve!: (value: unknown) => void;
    talkSvc.Say.mockImplementationOnce(() => new Promise(r => { resolve = r; }));
    shown(id);
    await setTalk(id, { composer: 'working' });
    await type('Please check the logout path');
    await fireEvent.keyDown(field(), { key: 'Enter' });
    expect(field().value).toBe('');
    expect(ssh.view(id).sending?.text).toBe('Please check the logout path');
    expect(document.querySelector('[data-footer-primary]')).toHaveAttribute('aria-busy', 'true');
    await type('And the settings screen');
    await fireEvent.keyDown(field(), { key: 'Enter' });
    expect(talkSvc.Say).toHaveBeenCalledTimes(1);
    resolve({ item: 'sent-1', delivered: true });
    await waitFor(() => expect(document.querySelector('[data-footer-primary]')).not.toHaveAttribute('aria-busy'));
    expect(field().value).toBe('And the settings screen');
    expect(ssh.view(id).outgoing).toEqual([{ text: 'Please check the logout path', conn: '', item: 'sent-1', delivered: true }]);
  });

  it('restores a failed message alongside anything typed while it was sending', async () => {
    const id = freshSession();
    let reject!: (error: Error) => void;
    talkSvc.Say.mockImplementationOnce(() => new Promise((_r, fail) => { reject = fail; }));
    shown(id);
    await setTalk(id);
    await type('original message');
    await fireEvent.keyDown(field(), { key: 'Enter' });
    await type('new thought');
    reject(new Error('connection lost'));
    await waitFor(() => expect(field().value).toBe('original message\nnew thought'));
    expect(ssh.view(id).sending).toBeNull();
    expect(ssh.view(id).outgoing).toHaveLength(0);
    expect(screen.getByRole('status')).toHaveTextContent('connection lost');
  });
});

describe('image preparation feedback', () => {
  const pasteImage = () => fireEvent.paste(field(), { clipboardData: { getData: () => '' } });

  it('reserves the image immediately, allows typing, and blocks Send until it is ready', async () => {
    const id = freshSession();
    let resolve!: (value: unknown) => void;
    talkSvc.AttachClipboardImage.mockImplementationOnce(() => new Promise(r => { resolve = r; }));
    shown(id);
    await setTalk(id);
    await type('Look here: ');
    field().setSelectionRange(11, 11);
    await pasteImage();
    expect(screen.getByRole('status', { name: 'Preparing image 1' })).toBeInTheDocument();
    expect(field().value).toBe(`Look here: ${attachmentToken(1)}`);
    expect(document.querySelector('[data-attachment="image"]')).toHaveAttribute('aria-busy', 'true');
    await type(`Look here: ${attachmentToken(1)} please fix this`);
    await fireEvent.keyDown(field(), { key: 'Enter' });
    expect(talkSvc.Say).not.toHaveBeenCalled();
    resolve({ path: '/repo/.lola/images/one.png', thumb: 'data:image/png;base64,thumb' });
    await waitFor(() => expect(screen.getByAltText('Attachment 1')).toBeInTheDocument());
    expect(field().value).toBe(`Look here: ${attachmentToken(1)} please fix this`);
    await fireEvent.keyDown(field(), { key: 'Enter' });
    await waitFor(() => expect(talkSvc.Say).toHaveBeenCalledWith(expect.objectContaining({ text: 'Look here: /repo/.lola/images/one.png please fix this' })));
  });

  it('does not resurrect a removed image or overwrite a newer attachment with the same number', async () => {
    const id = freshSession();
    let old!: (value: unknown) => void;
    let next!: (value: unknown) => void;
    talkSvc.AttachClipboardImage.mockImplementationOnce(() => new Promise(r => { old = r; }));
    talkSvc.AttachClipboardImage.mockImplementationOnce(() => new Promise(r => { next = r; }));
    shown(id);
    await pasteImage();
    await fireEvent.click(screen.getByRole('button', { name: 'Remove attachment 1' }));
    await pasteImage();
    old({ path: '/old.png', thumb: 'old' });
    await tick();
    expect(ssh.view(id).pastes.get(1)?.loading).toBe(true);
    next({ path: '/new.png', thumb: 'new' });
    await waitFor(() => expect(ssh.view(id).pastes.get(1)?.value).toBe('/new.png'));
    expect(field().value).toBe(attachmentToken(1));
  });

  it('shows file preparation immediately and leaves the draft intact on failure', async () => {
    const id = freshSession();
    let reject!: (error: Error) => void;
    talkSvc.AttachImageFile.mockImplementationOnce(() => new Promise((_r, fail) => { reject = fail; }));
    shown(id);
    await setTalk(id);
    await fireEvent.paste(field(), { clipboardData: { getData: () => '/tmp/screenshot.png' } });
    expect(screen.getByRole('status', { name: 'Preparing image 1' })).toBeInTheDocument();
    await type(`Please inspect ${attachmentToken(1)}`);
    reject(new Error('file unavailable'));
    await waitFor(() => expect(screen.getByRole('img', { name: 'Image 1 failed to attach' })).toBeInTheDocument());
    expect(field().value).toBe(`Please inspect ${attachmentToken(1)}`);
    expect(document.querySelector('[data-footer-primary]')).toBeDisabled();
    await fireEvent.click(screen.getByRole('button', { name: 'Remove attachment 1' }));
    expect(field().value).toBe('Please inspect ');
    expect(document.querySelector('[data-footer-primary]')).toBeEnabled();
  });

  it('finishes in the original session without touching a newly selected composer', async () => {
    const id = freshSession();
    let resolve!: (value: unknown) => void;
    talkSvc.AttachClipboardImage.mockImplementationOnce(() => new Promise(r => { resolve = r; }));
    const ui = shown(id);
    await pasteImage();
    const second = freshSession();
    await ui.rerender({ sessionId: second });
    await type('another session');
    resolve({ path: '/first-session.png', thumb: 'first' });
    await waitFor(() => expect(ssh.view(id).pastes.get(1)?.value).toBe('/first-session.png'));
    expect(field().value).toBe('another session');
    expect(ssh.view(second).pastes.size).toBe(0);
  });
});
