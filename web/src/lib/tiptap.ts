// Plain-text extraction from a TipTap JSON doc. Used for comment bodies
// and history previews — cheap, no editor instance needed.

const BLOCK_TYPES = new Set([
  "paragraph",
  "heading",
  "listItem",
  "blockquote",
  "codeBlock",
]);

/** Text of one block node: all descendant text nodes joined with spaces. */
function blockText(node: unknown): string {
  const parts: string[] = [];
  function walk(n: unknown) {
    if (n === null || typeof n !== "object") return;
    const rec = n as Record<string, unknown>;
    if (rec.type === "text" && typeof rec.text === "string") {
      parts.push(rec.text);
      return;
    }
    if (Array.isArray(rec.content)) {
      for (const child of rec.content) walk(child);
    }
  }
  walk(node);
  return parts.join("");
}

/** Extracts readable text from a TipTap doc JSON, one block per line. */
export function tiptapText(doc: unknown): string {
  const blocks: string[] = [];
  function walk(n: unknown) {
    if (n === null || typeof n !== "object") return;
    const rec = n as Record<string, unknown>;
    if (typeof rec.type === "string" && BLOCK_TYPES.has(rec.type)) {
      const t = blockText(n).trim();
      if (t) blocks.push(t);
      return;
    }
    if (Array.isArray(rec.content)) {
      for (const child of rec.content) walk(child);
    }
  }
  walk(doc);
  return blocks.join("\n");
}
