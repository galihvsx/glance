import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  readRecentIssues,
  recordRecentIssue,
} from "./recents";

const entry = (id: string) => ({
  id,
  name: `Issue ${id}`,
  display_id: `ENG-${id}`,
  slug: "acme",
  identifier: "ENG",
});

describe("recents", () => {
  beforeEach(() => {
    // Node test env has no jsdom: minimal in-memory localStorage stub.
    const store = new Map<string, string>();
    vi.stubGlobal("localStorage", {
      getItem: (k: string) => (store.has(k) ? store.get(k)! : null),
      setItem: (k: string, v: string) => void store.set(k, v),
      removeItem: (k: string) => void store.delete(k),
      clear: () => store.clear(),
    });
  });

  it("returns empty when nothing stored", () => {
    expect(readRecentIssues()).toEqual([]);
  });

  it("keeps most recent first and dedupes by id", () => {
    recordRecentIssue(entry("1"));
    recordRecentIssue(entry("2"));
    recordRecentIssue(entry("1"));
    const recents = readRecentIssues();
    expect(recents.map((r) => r.id)).toEqual(["1", "2"]);
  });

  it("caps at 5 entries", () => {
    for (let i = 1; i <= 7; i++) recordRecentIssue(entry(String(i)));
    const recents = readRecentIssues();
    expect(recents).toHaveLength(5);
    expect(recents[0].id).toBe("7");
  });

  it("ignores malformed stored data", () => {
    localStorage.setItem("glance:recents", "not-json");
    expect(readRecentIssues()).toEqual([]);
    localStorage.setItem(
      "glance:recents",
      JSON.stringify([{ id: 42, slug: "acme" }]),
    );
    expect(readRecentIssues()).toEqual([]);
  });
});
