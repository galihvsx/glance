// @-mention client helpers (C8T1): trigger detection, member filter,
// insertion, and mention-chip segmentation. Mirrors the backend
// resolution rules in internal/service/mention.go.
import { describe, expect, it } from "vitest";
import {
  applyMention,
  filterMentionMembers,
  findMentionTrigger,
  mentionLabel,
  renderMentionSegments,
  type MentionableMember,
} from "./mentions";

const MEMBERS: MentionableMember[] = [
  { id: "u-galih", name: "Galih Putro", email: "galih.putro@example.com" },
  { id: "u-budi", name: "budi", email: "budi.santoso@example.com" },
  { id: "u-noname", name: null, email: "ops.bot@example.com" },
];

describe("mentionLabel", () => {
  it("prefers name, falls back to email local-part", () => {
    expect(mentionLabel(MEMBERS[0])).toBe("Galih Putro");
    expect(mentionLabel(MEMBERS[2])).toBe("ops.bot");
  });
});

describe("findMentionTrigger", () => {
  it("finds @query before the caret", () => {
    expect(findMentionTrigger("hi @gal", 7)).toEqual({ start: 3, query: "gal" });
  });
  it("allows a bare @", () => {
    expect(findMentionTrigger("hi @", 4)).toEqual({ start: 3, query: "" });
  });
  it("rejects @ inside an email address", () => {
    expect(findMentionTrigger("mail galih@example.com", 21)).toBeNull();
  });
  it("rejects when the caret left the token", () => {
    expect(findMentionTrigger("hi @gal x", 9)).toBeNull();
  });
  it("handles caret mid-token", () => {
    expect(findMentionTrigger("hi @galih", 5)).toEqual({ start: 3, query: "g" });
  });
  it("returns null with no @", () => {
    expect(findMentionTrigger("hello", 5)).toBeNull();
  });
});

describe("filterMentionMembers", () => {
  it("matches name case-insensitively", () => {
    expect(filterMentionMembers(MEMBERS, "GAL").map((m) => m.id)).toEqual([
      "u-galih",
    ]);
  });
  it("matches email", () => {
    expect(filterMentionMembers(MEMBERS, "ops.bot").map((m) => m.id)).toEqual([
      "u-noname",
    ]);
  });
  it("empty query returns members capped at 8", () => {
    expect(filterMentionMembers(MEMBERS, "").length).toBe(3);
  });
});

describe("applyMention", () => {
  it("replaces @query with @Label plus trailing space", () => {
    const r = applyMention("hi @gal there", 3, 7, MEMBERS[0]);
    expect(r.text).toBe("hi @Galih Putro  there");
    expect(r.caret).toBe(3 + "@Galih Putro ".length);
  });
});

describe("renderMentionSegments", () => {
  it("chips a multi-word name mention", () => {
    const segs = renderMentionSegments("hey @Galih Putro review", MEMBERS);
    expect(segs).toEqual([
      { kind: "text", text: "hey " },
      { kind: "mention", id: "u-galih", label: "Galih Putro" },
      { kind: "text", text: " review" },
    ]);
  });
  it("chips case-insensitively", () => {
    const segs = renderMentionSegments("hi @BUDI", MEMBERS);
    expect(segs[1]).toEqual({ kind: "mention", id: "u-budi", label: "budi" });
  });
  it("chips via email local-part", () => {
    const segs = renderMentionSegments("hi @ops.bot", MEMBERS);
    expect(segs[1]).toEqual({
      kind: "mention",
      id: "u-noname",
      label: "ops.bot",
    });
  });
  it("does not chip email addresses", () => {
    const segs = renderMentionSegments("mail galih.putro@example.com", MEMBERS);
    expect(segs).toEqual([
      { kind: "text", text: "mail galih.putro@example.com" },
    ]);
  });
  it("does not chip ambiguous names", () => {
    const dupes: MentionableMember[] = [
      { id: "u-1", name: "Budi", email: "b1@example.com" },
      { id: "u-2", name: "budi", email: "b2@example.com" },
    ];
    const segs = renderMentionSegments("hi @budi", dupes);
    expect(segs).toEqual([{ kind: "text", text: "hi @budi" }]);
  });
  it("does not chip partial multi-word names", () => {
    const segs = renderMentionSegments("@Galih review", MEMBERS);
    expect(segs).toEqual([{ kind: "text", text: "@Galih review" }]);
  });
  it("handles multiple mentions", () => {
    const segs = renderMentionSegments("@budi and @Galih Putro", MEMBERS);
    expect(segs.filter((s) => s.kind === "mention").length).toBe(2);
  });
  it("returns empty for empty text", () => {
    expect(renderMentionSegments("", MEMBERS)).toEqual([]);
  });
});
