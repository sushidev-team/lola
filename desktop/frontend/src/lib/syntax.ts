import { langOf, tokenize, type Token } from "./diff";

export type SyntaxToken = { kind: Token["kind"] | "flag" | "var" | "cmd" | "path"; text: string };
const aliases: Record<string, string> = { javascript: "js", typescript: "ts", python: "py", ruby: "rb", shell: "sh", shellscript: "sh", console: "sh", json: "js", jsonc: "js", csharp: "cs", rust: "rs", golang: "go" };
const commands = new Set("git gh cd pwd ls cat rg grep find sed awk head tail echo printf npm npx pnpm yarn bun node python python3 go make php composer curl docker ssh sudo kill test export source".split(" "));

/** Text tokens only: no HTML, no changes to copied commands, bounded work for large logs. */
export function syntaxTokens(text: string, language = "", path = ""): SyntaxToken[] {
  if (/^[A-Za-z0-9_./:@~-]+$/.test(text) && !/^\d+(?:\.\d+)?$/.test(text)) return [{ kind: "plain", text }];
  if (text.length > 48_000) return [{ kind: "plain", text }];
  const ext = aliases[language.toLowerCase()] ?? language.toLowerCase();
  const lang = path ? langOf(path) : langOf(`code.${ext || "js"}`);
  const shell = ["sh", "bash", "zsh"].includes(ext);
  const out: SyntaxToken[] = [];
  const lines = text.split("\n");
  lines.forEach((line, index) => {
    if (index) out.push({ kind: "plain", text: "\n" });
    for (const token of tokenize(line, lang)) {
      if (!shell || token.kind !== "plain") { out.push(token); continue; }
      const re = /\$\{[^}]*\}|\$[A-Za-z_][\w]*|--?[A-Za-z][\w-]*|(?:\.?\.?\/|~\/)[^\s;|&]+|\b[A-Za-z][\w-]*\b/g;
      let end = 0;
      for (const match of token.text.matchAll(re)) {
        const at = match.index!;
        if (at > end) out.push({ kind: "plain", text: token.text.slice(end, at) });
        const word = match[0];
        const kind = word.startsWith("$") ? "var" : word.startsWith("-") ? "flag" : word.includes("/") ? "path" : commands.has(word) ? "cmd" : "plain";
        out.push({ kind, text: word });
        end = at + word.length;
      }
      if (end < token.text.length) out.push({ kind: "plain", text: token.text.slice(end) });
    }
  });
  return out;
}
