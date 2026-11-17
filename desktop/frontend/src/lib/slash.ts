// What a line starting with "/" or "!" means before it goes to the agent. Pure, so each rule
// can be read and tested on its own.
//
// These rules come from measuring claude-code. An unknown /word loses the text entirely, a
// screen command opens a dialog that only takes keys, and a leading ! runs a shell on this
// machine. So a prefix is never sent, a screen is opened in the terminal instead of typed, and
// an unknown word asks once before it goes.
//
// Only /config, /usage, /cost and the help screens were measured as screens. The rest of that
// list is judgement that errs on the safe side: wrongly calling something a screen costs a
// person one trip to the terminal, and wrongly typing one leaves a dialog open on the pane.
//
// Codex and opencode have empty tables because nobody has measured them, so each of their
// /words takes the unknown path.

export type SlashTable = { inline: readonly string[]; screen: readonly string[] };

const CLAUDE: SlashTable = {
  inline: ["/compact", "/context", "/rename", "/clear"],
  screen: [
    "/config", "/usage", "/cost", "/help", "/model", "/resume", "/agents", "/mcp", "/permissions", "/login", "/logout",
    "/doctor", "/memory", "/hooks", "/rewind", "/status", "/theme", "/vim", "/terminal-setup",
  ],
};
const NONE: SlashTable = { inline: [], screen: [] };

export function slashTable(agent: string | undefined): SlashTable {
  return !agent || agent === "claude" ? CLAUDE : NONE;
}

/** The first word of a slash line, or "" when the line is not one. `/tmp/foo` is a sentence. */
export function slashWord(text: string): string {
  return /^\/[A-Za-z][A-Za-z0-9_:-]*(?=\s|$)/.exec(text)?.[0] ?? "";
}

export type SlashRow = { name: string; screen: boolean };

/**
 * The list under the field: known commands starting with what is typed, from column 0. It
 * closes at the first space, because by then the human is writing the command's arguments.
 */
export function slashRows(text: string, agent: string | undefined): SlashRow[] {
  if (!/^\/[A-Za-z0-9_:-]*$/.test(text)) return [];
  const t = slashTable(agent);
  const rows = [...t.inline.map((name) => ({ name, screen: false })), ...t.screen.map((name) => ({ name, screen: true }))];
  return rows.filter((r) => r.name.startsWith(text));
}

export type SlashEnter =
  | { act: "send" }
  | { act: "fill"; text: string }
  | { act: "terminal"; word: string }
  | { act: "warn"; word: string };

/**
 * What ↩ does with this text. `picked` is the row the list stands on, and `warned` is the word
 * lola already warned about, so the second ↩ on the same word sends and a new word warns again.
 */
export function slashEnter(raw: string, agent: string | undefined, picked = "", warned = ""): SlashEnter {
  // The daemon trims before it reads a line as a command, so this reads the same line.
  const text = raw.trim();
  if (!text.startsWith("/")) return { act: "send" };
  const rows = slashRows(text, agent);
  // A row someone walked to is the answer, and so is a prefix: the prefix itself never goes.
  if (rows.length && picked && picked !== text) return { act: "fill", text: picked };
  const word = slashWord(text);
  const t = slashTable(agent);
  if (word && t.screen.includes(word)) return { act: "terminal", word };
  if (word && t.inline.includes(word)) return { act: "send" };
  if (rows.length) return { act: "fill", text: rows[0].name };
  if (!word) return { act: "send" };
  return word === warned ? { act: "send" } : { act: "warn", word };
}

export function unknownSlashNote(word: string, agent: string): string {
  return `lola doesn't know ${word}. ${agent} drops the text if it doesn't either.`;
}

export const BANG_NOTE = "Sent as text. To run on this machine, use the terminal.";

/** The daemon adds the leading space that neutralizes this. The footer only says so. */
export function isBang(text: string): boolean {
  return /^\s*!/.test(text);
}
