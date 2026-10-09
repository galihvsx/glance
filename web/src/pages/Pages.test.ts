import { describe, expect, it } from "vitest";
import { buildPageTree, renderMarkdown } from "./Pages";
import type { Page } from "../lib/types";

function page(partial: Partial<Page> & { id: string }): Page {
  return {
    project_id: "proj-1",
    title: "t",
    content: "",
    position: 0,
    created_at: "2026-10-09T00:00:00Z",
    updated_at: "2026-10-09T00:00:00Z",
    ...partial,
  };
}

describe("buildPageTree", () => {
  it("nests children under parents and sorts by position", () => {
    const pages = [
      page({ id: "c2", parent_id: "r", position: 1, title: "second" }),
      page({ id: "r", position: 0, title: "root" }),
      page({ id: "c1", parent_id: "r", position: 0, title: "first" }),
      page({ id: "g", parent_id: "c1", position: 0, title: "grandchild" }),
    ];
    const tree = buildPageTree(pages);
    expect(tree).toHaveLength(1);
    expect(tree[0].page.id).toBe("r");
    expect(tree[0].depth).toBe(0);
    expect(tree[0].children.map((n) => n.page.id)).toEqual(["c1", "c2"]);
    expect(tree[0].children[0].children[0].page.id).toBe("g");
    expect(tree[0].children[0].children[0].depth).toBe(2);
  });

  it("treats orphaned parent_ids as roots", () => {
    const tree = buildPageTree([
      page({ id: "a", parent_id: "missing" }),
      page({ id: "b" }),
    ]);
    expect(tree.map((n) => n.page.id).sort()).toEqual(["a", "b"]);
  });
});

describe("renderMarkdown", () => {
  it("renders headings, bold, italic, code, links, lists", () => {
    const html = renderMarkdown(
      "# Title\n\nHello **bold** and *italic* with `code`.\n\n- one\n- two\n\n1. first\n\n[link](https://example.com)",
    );
    expect(html).toContain("<h1>Title</h1>");
    expect(html).toContain("<strong>bold</strong>");
    expect(html).toContain("<em>italic</em>");
    expect(html).toContain("<code>code</code>");
    expect(html).toContain("<ul>");
    expect(html).toContain("<ol>");
    expect(html).toContain('<a href="https://example.com"');
  });

  it("escapes HTML and blocks javascript: links", () => {
    const html = renderMarkdown(
      '<script>alert("x")</script>\n\n[evil](javascript:alert(1))',
    );
    expect(html).not.toContain("<script>");
    expect(html).toContain("&lt;script&gt;");
    expect(html).not.toContain("javascript:");
  });

  it("renders fenced code blocks and blockquotes", () => {
    const html = renderMarkdown("```\nconst x = 1;\n```\n\n> quoted");
    expect(html).toContain("<pre><code>");
    expect(html).toContain("const x = 1;");
    expect(html).toContain("<blockquote>quoted</blockquote>");
  });
});
