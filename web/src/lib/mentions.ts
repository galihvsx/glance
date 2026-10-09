// @-mention helpers for comments (C8T1). Client-side mirror of the
// backend resolution rules in internal/service/mention.go:
// case-insensitive match against member name (multi-word names via
// longest-prefix at each @), email local-part as fallback, ambiguous
// matches skipped, email addresses never parsed as mentions.

export interface MentionableMember {
  id: string;
  name?: string | null;
  email: string;
}

function tokenChar(ch: string): boolean {
  return /[\p{L}\p{N}_.-]/u.test(ch);
}

function wordChar(ch: string): boolean {
  return /[\p{L}\p{N}_]/u.test(ch);
}

/** Display label for a member in the autocomplete: name, else local-part. */
export function mentionLabel(m: MentionableMember): string {
  const name = (m.name ?? "").trim();
  if (name) return name;
  const at = m.email.indexOf("@");
  return at >= 0 ? m.email.slice(0, at) : m.email;
}

/**
 * Find the @-trigger immediately before the caret. Returns the @ offset
 * and the typed query, or null when the caret is not inside a mention.
 * The @ must not be preceded by a word char (kills user@domain).
 */
export function findMentionTrigger(
  text: string,
  caret: number,
): { start: number; query: string } | null {
  let i = Math.min(caret, text.length) - 1;
  while (i >= 0 && tokenChar(text[i])) i--;
  if (i < 0 || text[i] !== "@") return null;
  const prev = i > 0 ? text[i - 1] : "";
  if (prev !== "" && (wordChar(prev) || prev === "." || prev === "@")) {
    return null;
  }
  return { start: i, query: text.slice(i + 1, caret) };
}

/** Case-insensitive substring filter over name + email. */
export function filterMentionMembers(
  members: MentionableMember[],
  query: string,
): MentionableMember[] {
  const q = query.trim().toLowerCase();
  if (!q) return members.slice(0, 8);
  return members
    .filter(
      (m) =>
        (m.name ?? "").toLowerCase().includes(q) ||
        m.email.toLowerCase().includes(q),
    )
    .slice(0, 8);
}

/** Replace the @query span with @Label + trailing space; returns the new
 *  caret position. */
export function applyMention(
  text: string,
  triggerStart: number,
  caret: number,
  member: MentionableMember,
): { text: string; caret: number } {
  const insert = `@${mentionLabel(member)} `;
  const next = text.slice(0, triggerStart) + insert + text.slice(caret);
  return { text: next, caret: triggerStart + insert.length };
}

export type MentionSegment =
  | { kind: "text"; text: string }
  | { kind: "mention"; id: string; label: string };

function normalizedKey(s: string): string {
  return s.trim().toLowerCase();
}

function localPart(email: string): string {
  const at = email.indexOf("@");
  return (at >= 0 ? email.slice(0, at) : email).toLowerCase();
}

/**
 * Split rendered comment text into plain spans and mention chips. Mirrors
 * backend ResolveMentions: longest multi-word name prefix at each @
 * (boundary required after the name), else @token against names and
 * email local-parts. Ambiguous keys are not chipped.
 */
export function renderMentionSegments(
  text: string,
  members: MentionableMember[],
): MentionSegment[] {
  const segments: MentionSegment[] = [];
  if (!text || members.length === 0) {
    return text ? [{ kind: "text", text }] : [];
  }

  const nameToId = new Map<string, string>();
  const localToId = new Map<string, string>();
  const ambiguous = new Set<string>();
  const names: Array<{ key: string; id: string }> = [];
  for (const m of members) {
    const nk = normalizedKey(m.name ?? "");
    if (nk) {
      if (nameToId.has(nk)) {
        ambiguous.add(`n:${nk}`);
        nameToId.delete(nk);
      } else if (!ambiguous.has(`n:${nk}`)) {
        nameToId.set(nk, m.id);
      }
      names.push({ key: nk, id: m.id });
    }
    const lp = localPart(m.email);
    if (lp) {
      if (localToId.has(lp)) {
        ambiguous.add(`l:${lp}`);
        localToId.delete(lp);
      } else if (!ambiguous.has(`l:${lp}`)) {
        localToId.set(lp, m.id);
      }
    }
  }

  const lowered = text.toLowerCase();
  let pos = 0; // consumed offset
  let buf = "";
  const flush = () => {
    if (buf) {
      segments.push({ kind: "text", text: buf });
      buf = "";
    }
  };

  for (let i = 0; i < text.length; i++) {
    if (text[i] !== "@") continue;
    const prev = i > 0 ? text[i - 1] : "";
    if (prev !== "" && (wordChar(prev) || prev === "." || prev === "@")) {
      continue; // email address, not a mention
    }
    const rest = lowered.slice(i + 1);
    // Longest multi-word name prefix.
    let best: { key: string; id: string } | null = null;
    for (const n of names) {
      if (ambiguous.has(`n:${n.key}`)) continue;
      if (!rest.startsWith(n.key)) continue;
      const after = rest.slice(n.key.length);
      if (after !== "" && wordChar(after[0])) continue;
      if (!best || n.key.length > best.key.length) best = n;
    }
    let matchId: string | null = null;
    let matchLen = 0;
    if (best) {
      matchId = best.id;
      matchLen = best.key.length;
    } else {
      let j = i + 1;
      while (j < text.length && tokenChar(text[j])) j++;
      const token = normalizedKey(text.slice(i + 1, j).replace(/[.-]+$/, ""));
      if (token) {
        const id = nameToId.get(token) ?? localToId.get(token) ?? null;
        if (id) {
          matchId = id;
          matchLen = token.length;
        }
      }
    }
    if (!matchId) continue;
    const member = members.find((m) => m.id === matchId);
    if (!member) continue;
    buf += text.slice(pos, i);
    flush();
    segments.push({ kind: "mention", id: member.id, label: mentionLabel(member) });
    i += matchLen; // consume "@" + matched name
    pos = i + 1;
  }
  buf += text.slice(pos);
  flush();
  return segments;
}
