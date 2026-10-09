// Tiny markdown renderer (moved verbatim from pages/Pages.tsx, C3T4).
// Lean by design: no new deps (single-binary thesis). HTML-escapes
// everything first, then applies inline formatting; link hrefs are
// allow-listed to http(s)/mailto. Shared by wiki pages and the project
// overview description.

/* ------------------------------------------------------------------ */
/* Tiny markdown renderer (C3T4). Lean by design: no new deps (single-  */
/* binary thesis). HTML-escapes everything first, then applies inline  */
/* formatting; link hrefs are allow-listed to http(s)/mailto.          */
/* ------------------------------------------------------------------ */

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function safeHref(raw: string): string | null {
  const t = raw.trim();
  if (/^(https?:\/\/|mailto:)/i.test(t)) return escapeHtml(t);
  return null;
}

function inlineMd(s: string): string {
  let out = escapeHtml(s);
  out = out.replace(/`([^`]+)`/g, "<code>$1</code>");
  out = out.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  out = out.replace(/(^|[^*\w])\*([^*\n]+)\*/g, "$1<em>$2</em>");
  out = out.replace(
    /\[([^\]]+)\]\(([^)\s]+)\)/g,
    (_m, text: string, href: string) => {
      const safe = safeHref(href);
      return safe
        ? `<a href="${safe}" target="_blank" rel="noreferrer">${text}</a>`
        : text;
    },
  );
  return out;
}

/** Block-level markdown → HTML. One pass over lines, grouping blocks. */
export function renderMarkdown(src: string): string {
  const lines = src.replace(/\r\n?/g, "\n").split("\n");
  const html: string[] = [];
  let i = 0;

  const flushList = (items: string[], ordered: boolean) => {
    const tag = ordered ? "ol" : "ul";
    html.push(
      `<${tag}>${items.map((it) => `<li>${inlineMd(it)}</li>`).join("")}</${tag}>`,
    );
  };

  while (i < lines.length) {
    const line = lines[i];
    // Fenced code block
    if (/^```/.test(line)) {
      const buf: string[] = [];
      i++;
      while (i < lines.length && !/^```/.test(lines[i])) {
        buf.push(lines[i]);
        i++;
      }
      i++; // consume closing fence (or EOF)
      html.push(`<pre><code>${escapeHtml(buf.join("\n"))}</code></pre>`);
      continue;
    }
    // ATX heading
    const hm = /^(#{1,3})\s+(.*)$/.exec(line);
    if (hm) {
      const level = hm[1].length;
      html.push(`<h${level}>${inlineMd(hm[2])}</h${level}>`);
      i++;
      continue;
    }
    // Horizontal rule
    if (/^\s*---+\s*$/.test(line)) {
      html.push("<hr />");
      i++;
      continue;
    }
    // Blockquote
    if (/^\s*>/.test(line)) {
      const buf: string[] = [];
      while (i < lines.length && /^\s*>/.test(lines[i])) {
        buf.push(lines[i].replace(/^\s*> ?/, ""));
        i++;
      }
      html.push(`<blockquote>${inlineMd(buf.join(" "))}</blockquote>`);
      continue;
    }
    // Unordered list
    if (/^\s*[-*]\s+/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^\s*[-*]\s+/.test(lines[i])) {
        items.push(lines[i].replace(/^\s*[-*]\s+/, ""));
        i++;
      }
      flushList(items, false);
      continue;
    }
    // Ordered list
    if (/^\s*\d+\.\s+/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^\s*\d+\.\s+/.test(lines[i])) {
        items.push(lines[i].replace(/^\s*\d+\.\s+/, ""));
        i++;
      }
      flushList(items, true);
      continue;
    }
    // Blank line: paragraph separator
    if (/^\s*$/.test(line)) {
      i++;
      continue;
    }
    // Paragraph: run of non-blank, non-block lines
    const buf: string[] = [];
    while (
      i < lines.length &&
      !/^\s*$/.test(lines[i]) &&
      !/^(```|#{1,3}\s|>\s*|[-*]\s+|\d+\.\s+|---+)/.test(lines[i])
    ) {
      buf.push(lines[i]);
      i++;
    }
    html.push(`<p>${inlineMd(buf.join(" "))}</p>`);
  }
  return html.join("\n");
}
