import { cssCompletionSource, cssLanguage } from "@codemirror/lang-css";
import type { CompletionSource } from "@codemirror/autocomplete";

// Parse in declaration context, just like the contents of a style attribute.
export const inlineCSS = cssLanguage.configure({ top: "Styles" });

export function readDeclaration(text: string, inline: string[][]) {
  const colon = text.indexOf(":");
  const property = (colon < 0 ? text : text.slice(0, colon)).trim();
  const name = property.startsWith("--") ? property : property.toLowerCase();
  const end = inlineCSS.parser.parse(text).topNode.getChild(";");
  const value = colon < 0 ? "" : text.slice(colon + 1, end?.from).trim();
  const bare = value.replace(/\s*!important\s*$/i, "").trim();
  const existing = inline.some(([key]) => key === name);
  let error = "";
  if (end && text.slice(end.to).trim()) error = "Edit one declaration at a time.";
  else if (!name || !CSS.supports(name, "initial")) error = "Unknown CSS property.";
  else if (colon < 0) error = "Add a colon and a value.";
  else if (!value && !existing) error = "Enter a value for " + name + ".";
  else if (value && (!bare || !CSS.supports(name, bare))) error = "Invalid value for " + name + ".";
  return { name, value, error, remove: !value && existing && colon >= 0 };
}

// Reuse the templates editor's keyword list, narrowed to values the browser
// accepts for this property. Inside functions or compound values, retain the
// language provider's token-level completions.
export const completeDeclaration: CompletionSource = async context => {
  const result = await cssCompletionSource(context);
  if (!result) return null;
  const after = context.state.sliceDoc(context.pos);
  const separator = /^[\w-]*:\s*/.exec(after);
  if (separator && result.options[0]?.type === "property") {
    return { ...result, to: context.pos + separator[0].length };
  }
  const before = context.state.sliceDoc(0, result.from);
  const match = /^\s*([\w-]+)\s*:\s*$/.exec(before);
  if (!match || match[1].startsWith("--")) return result;
  const options = result.options.filter(({ label }) =>
    CSS.supports(match[1], label) || CSS.supports(match[1], label + "(1px)"));
  return { ...result, options };
};
