// The only module that knows Capacitor.
//
// Invariants:
//  - Availability comes from `Capacitor.isPluginAvailable`, never from probing
//    `Capacitor.Plugins`: that object is a Proxy that invents a function for
//    every property name, so a probe finds "something" on every platform.
//  - The plugin proxies exported here are thenables (a `then` property reads as
//    a bridge method). Never return one from an async function or resolve a
//    promise with one; that hangs forever. Bind them at module level and hand
//    them out by reference only.
//  - Off a device (browser, vitest) `native`, `keyboard` and `browser` are
//    null, and `transport` is the rejecting web stand-in.

import { Browser } from "@capacitor/browser";
import { Capacitor, registerPlugin } from "@capacitor/core";
import { Keyboard } from "@capacitor/keyboard";
import type { LolaTransportPlugin } from "lola-transport";

import { webTransport } from "./web";

export type { LolaTransportPlugin } from "lola-transport";

type KeyboardPlugin = typeof Keyboard;
type BrowserPlugin = typeof Browser;

function available(name: string): boolean {
  try {
    return Capacitor.isPluginAvailable(name);
  } catch {
    return false;
  }
}

/** The native transport, or null when this build has none. */
export const native: LolaTransportPlugin | null = available("LolaTransport")
  ? registerPlugin<LolaTransportPlugin>("LolaTransport")
  : null;

/** Always callable: the native transport, else a stand-in whose device methods reject. */
export const transport: LolaTransportPlugin = native ?? webTransport;

export const keyboard: KeyboardPlugin | null = available("Keyboard") ? Keyboard : null;

export const browser: BrowserPlugin | null = available("Browser") ? Browser : null;
