// Pure-helper tests for the page↔issue link sections (C36T1): the
// create-from-page payload shape, the tree-flattened page picker order,
// and the link-issue picker scoping.

import { describe, expect, it } from "vitest";
import {
  buildCreateFromPageDescription,
  buildCreateFromPageIssueBody,
  filterLinkableIssues,
  flattenPagesForPicker,
  pageUrlForPage,
} from "./pageIssueLinks";
import type { Issue, Page } from "./types";

function page(
  id: string,
  title: string,
  parent_id: string | null,
  position: number,
): Page {
  return {
    id,
    project_id: "proj",
    parent_id,
    title,
    content: "",
    position,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}

function issue(
  id: string,
  overrides: Partial<Issue> = {},
): Issue {
  return {
    id,
    project_id: "proj",
    sequence_id: 1,
    display_id: "ENG-1",
    name: "Issue",
    priority: 0,
    state_id: "s1",
    sort_order: 0,
    is_draft: false,
    created_by: "u1",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    assignees: [],
    labels: [],
    ...overrides,
  };
}

describe("buildCreateFromPageDescription", () => {
  it("is 'From page: <title>' plus the page URL", () => {
    expect(
      buildCreateFromPageDescription(
        "API auth runbook",
        "https://app.example/w/ws/p/ENG/pages",
      ),
    ).toBe(
      "From page: API auth runbook\nhttps://app.example/w/ws/p/ENG/pages",
    );
  });
});

describe("buildCreateFromPageIssueBody", () => {
  it("posts title = page title and a TipTap doc description", () => {
    const body = buildCreateFromPageIssueBody(
      "API auth runbook",
      "https://app.example/w/ws/p/ENG/pages",
    );
    expect(body.name).toBe("API auth runbook");
    const doc = body.description as {
      type: string;
      content: { content: { text: string }[] }[];
    };
    expect(doc.type).toBe("doc");
    expect(doc.content.map((p) => p.content[0].text)).toEqual([
      "From page: API auth runbook",
      "https://app.example/w/ws/p/ENG/pages",
    ]);
  });
});

describe("pageUrlForPage", () => {
  it("builds the project's Pages-view URL", () => {
    expect(pageUrlForPage("ws", "ENG", "https://app.example")).toBe(
      "https://app.example/w/ws/p/ENG/pages",
    );
  });
});

describe("flattenPagesForPicker", () => {
  it("flattens depth-first, siblings by (position, title), with depth", () => {
    const pages = [
      page("b", "B root", null, 2),
      page("a", "A root", null, 1),
      page("a2", "A child 2", "a", 2),
      page("a1", "A child 1", "a", 1),
      page("a1x", "A grandchild", "a1", 1),
    ];
    const flat = flattenPagesForPicker(pages);
    expect(flat.map((f) => [f.page.id, f.depth])).toEqual([
      ["a", 0],
      ["a1", 1],
      ["a1x", 2],
      ["a2", 1],
      ["b", 0],
    ]);
  });

  it("treats orphans as roots and never loops on a parent cycle", () => {
    const pages = [
      page("orphan", "Orphan", "missing", 1),
      page("x", "X", "y", 1),
      page("y", "Y", "x", 1),
    ];
    const flat = flattenPagesForPicker(pages);
    expect(flat.map((f) => f.page.id).sort()).toEqual([
      "orphan",
      "x",
      "y",
    ]);
    expect(flat).toHaveLength(3);
  });
});

describe("filterLinkableIssues", () => {
  it("excludes already-linked, draft, and archived issues", () => {
    const live = issue("live");
    const linked = issue("linked");
    const draft = issue("draft", { is_draft: true });
    const archived = issue("archived", {
      archived_at: "2026-01-02T00:00:00Z",
    });
    expect(
      filterLinkableIssues([live, linked, draft, archived], ["linked"]),
    ).toEqual([live]);
  });
});
