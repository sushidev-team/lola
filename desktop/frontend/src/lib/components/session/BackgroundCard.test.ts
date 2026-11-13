import { afterEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { freshSession, setTalk, talk, talkSvc } from "./talk.testkit";
import BackgroundCard from "./BackgroundCard.svelte";
import { composerState, tierOneCount, trayCards } from "$lib/composer";

afterEach(() => { cleanup(); vi.clearAllMocks(); });

const prompt = { kind: "background", hash: "running-build", lines: ["Bash(make test)"], choices: [{ key: "background", label: "Move to background" }] };

it("shows the command and sends the pinned action, with duplicate clicks blocked across remounts", async () => {
  const id = freshSession();
  await setTalk(id, { composer: "working", prompt });
  let finish!: (v: unknown) => void;
  talkSvc.Choose.mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
  const first = render(BackgroundCard, { sessionId: id, prompt } as never);
  expect(screen.getByText("Bash(make test)")).toBeTruthy();
  await fireEvent.click(screen.getByRole("button", { name: "Move to background" }));
  expect(talkSvc.Choose).toHaveBeenCalledWith({ session: id, hash: prompt.hash, key: "background" });
  first.unmount();
  render(BackgroundCard, { sessionId: id, prompt } as never);
  expect((screen.getByRole("button", { name: "Moving to background…" }) as HTMLButtonElement).disabled).toBe(true);
  await talk.background(id, prompt.hash);
  expect(talkSvc.Choose).toHaveBeenCalledTimes(1);
  finish({ sent: true });
  await waitFor(() => expect(talk.backgroundPending[id]).toBe(false));
  expect(talkSvc.Say).not.toHaveBeenCalled();
});

it("shows a refusal and permits retry after a failed request", async () => {
  const id = freshSession();
  render(BackgroundCard, { sessionId: id, prompt } as never);
  talkSvc.Choose.mockResolvedValueOnce({ sent: false, why: "This command has changed." });
  await fireEvent.click(screen.getByRole("button", { name: "Move to background" }));
  expect(await screen.findByRole("status")).toHaveTextContent("This command has changed.");
  talkSvc.Choose.mockRejectedValueOnce(new Error("Connection lost"));
  await fireEvent.click(screen.getByRole("button", { name: "Move to background" }));
  await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Connection lost"));
  expect((screen.getByRole("button", { name: "Move to background" }) as HTMLButtonElement).disabled).toBe(false);
});

it("keeps the action behind approvals without making a working agent need an answer", () => {
  const cards = trayCards({ connections: [], items: [], activeConn: "", agentPrompt: prompt } as never);
  expect(cards[0].kind).toBe("background");
  expect(tierOneCount(cards)).toBe(0);
  expect(composerState({ alive: true, talk: { composer: "working", prompt, caps: { send: true } }, cardWaiting: tierOneCount(cards) > 0, held: 0 } as never)).toBe("working");
});

it("disables the action when the view cannot control the session", () => {
  render(BackgroundCard, { sessionId: freshSession(), prompt, disabled: true } as never);
  expect((screen.getByRole("button", { name: "Move to background" }) as HTMLButtonElement).disabled).toBe(true);
});
