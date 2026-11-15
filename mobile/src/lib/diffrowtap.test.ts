import { afterEach, describe, expect, it, vi } from "vitest";
import { diffRowTap } from "./diffrowtap";

function mount(): { host: HTMLElement; gutter: HTMLButtonElement; code: HTMLElement; link: HTMLElement; remove: HTMLButtonElement; hunk: HTMLElement; stop: () => void } {
  const host = document.createElement("div");
  host.innerHTML = `
    <div class="diff-row"><button class="line-gutter">1</button><span class="marker">+</span><div class="code">code</div></div>
    <div class="diff-row"><span class="hunk">@@</span></div>
    <div class="queued"><button class="rm">x</button></div>
    <div class="diff-row"><button class="line-gutter">2</button><a class="link" href="#x">l</a></div>`;
  document.body.append(host);
  const { destroy } = diffRowTap(host);
  const q = <T extends Element>(s: string) => host.querySelector(s) as T;
  return { host, gutter: q<HTMLButtonElement>(".line-gutter"), code: q(".code"), link: q(".link"), remove: q<HTMLButtonElement>(".rm"), hunk: q(".hunk"), stop: destroy };
}

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

describe("diffRowTap", () => {
  it("forwards a tap on the code to the row's gutter, once", () => {
    const m = mount();
    const click = vi.fn();
    m.gutter.addEventListener("click", click);
    m.code.click();
    expect(click).toHaveBeenCalledTimes(1);
  });

  it("does not loop when the gutter itself is tapped", () => {
    const m = mount();
    const click = vi.fn();
    m.gutter.addEventListener("click", click);
    m.gutter.click();
    expect(click).toHaveBeenCalledTimes(1);
  });

  it("leaves links and unrelated buttons alone", () => {
    const m = mount();
    const click = vi.fn();
    m.host.querySelectorAll(".line-gutter").forEach((g) => g.addEventListener("click", click));
    m.link.click();
    m.remove.click();
    expect(click).not.toHaveBeenCalled();
  });

  it("ignores a row with no gutter (hunk header)", () => {
    const m = mount();
    expect(() => m.hunk.click()).not.toThrow();
  });

  it("leaves a tap that ended a text selection alone", () => {
    const m = mount();
    const click = vi.fn();
    m.gutter.addEventListener("click", click);
    const range = document.createRange();
    range.selectNodeContents(m.code);
    getSelection()?.removeAllRanges();
    getSelection()?.addRange(range);
    m.code.click();
    expect(click).not.toHaveBeenCalled();
    getSelection()?.removeAllRanges();
  });

  it("stops listening when destroyed", () => {
    const m = mount();
    const click = vi.fn();
    m.gutter.addEventListener("click", click);
    m.stop();
    m.code.click();
    expect(click).not.toHaveBeenCalled();
  });
});
