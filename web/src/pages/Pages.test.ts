import { afterEach, describe, expect, it, vi } from "vitest";
import {
  buildPageExportDocument,
  buildPageTree,
  copyPageMarkdown,
  escapeYamlDoubleQuoted,
  pageExportFilename,
} from "./Pages";
import { copyText } from "../lib/clipboard";
import { renderMarkdown } from "../lib/markdown";
import { toast } from "../components/ui/toast";
import type { Page } from "../lib/types";

// Clipboard and toast are mocked: these tests cover the export wiring
// (which document is copied, toast states), not the browser clipboard.
vi.mock("../lib/clipboard", () => ({ copyText: vi.fn() }));
vi.mock("../components/ui/toast", () => ({ toast: { add: vi.fn() } }));

const mockCopyText = vi.mocked(copyText);
const mockToastAdd = vi.mocked(toast.add);

afterEach(() => {
  vi.clearAllMocks();
});

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

describe("pageExportFilename", () => {
  it("lowercases and turns spaces/punctuation into single dashes", () => {
    expect(pageExportFilename("Hello World!")).toBe("hello-world.md");
    expect(pageExportFilename("Sprint 42 Review")).toBe(
      "sprint-42-review.md",
    );
  });

  it("collapses runs of separators and trims leading/trailing dashes", () => {
    expect(pageExportFilename("  My  Page — Notes  ")).toBe(
      "my-page-notes.md",
    );
  });

  it("maps non-ASCII characters to dashes (ASCII charset pin)", () => {
    // é / ☕ are not a-z0-9, so each run becomes one dash.
    expect(pageExportFilename("Café ☕ Notes")).toBe("caf-notes.md");
    expect(pageExportFilename("Ünïcödé")).toBe("n-c-d.md");
  });

  it("falls back to page.md for empty / symbol-only / unicode-only titles", () => {
    expect(pageExportFilename("")).toBe("page.md");
    expect(pageExportFilename("   ")).toBe("page.md");
    expect(pageExportFilename("!!!")).toBe("page.md");
    expect(pageExportFilename("☕☕")).toBe("page.md");
  });

  it("never lets slashes or dot-dots escape into a path", () => {
    expect(pageExportFilename("../escape")).toBe("escape.md");
    expect(pageExportFilename("a/b\\c.md")).toBe("a-b-c-md.md");
  });
});

describe("escapeYamlDoubleQuoted", () => {
  it("leaves plain titles untouched", () => {
    expect(escapeYamlDoubleQuoted("plain title")).toBe("plain title");
  });

  it('escapes " as \\" and \\ as \\\\', () => {
    expect(escapeYamlDoubleQuoted('a"b\\c')).toBe('a\\"b\\\\c');
  });

  it("escapes backslashes before quotes (backslash-quote stays one unit)", () => {
    // Input is backslash + quote; escaping quotes first would double it.
    expect(escapeYamlDoubleQuoted('\\"')).toBe('\\\\\\"');
  });
});

describe("buildPageExportDocument", () => {
  it("emits the pinned front-matter + content shape", () => {
    const doc = buildPageExportDocument(
      page({
        id: "p1",
        title: "My Page",
        content: "# Hello\n\nBody",
        updated_at: "2026-10-16T04:00:00Z",
      }),
    );
    expect(doc).toBe(
      '---\ntitle: "My Page"\nupdated_at: "2026-10-16T04:00:00Z"\n---\n\n# Hello\n\nBody\n',
    );
  });

  it("escapes quotes and backslashes in the front-matter title", () => {
    const doc = buildPageExportDocument(
      page({
        id: "p1",
        title: 'Say "hi" \\ bye',
        content: "x",
        updated_at: "2026-10-16T04:00:00Z",
      }),
    );
    expect(doc).toContain('title: "Say \\"hi\\" \\\\ bye"');
  });

  it("keeps updated_at verbatim and ends empty content with a trailing newline", () => {
    const doc = buildPageExportDocument(
      page({
        id: "p1",
        title: "T",
        content: "",
        updated_at: "2026-10-16T04:00:00Z",
      }),
    );
    expect(doc).toBe(
      '---\ntitle: "T"\nupdated_at: "2026-10-16T04:00:00Z"\n---\n\n\n',
    );
  });
});

describe("copyPageMarkdown", () => {
  const p = page({
    id: "p1",
    title: "My Page",
    content: "# Hello",
    updated_at: "2026-10-16T04:00:00Z",
  });

  it("copies the byte-identical export document and toasts success", async () => {
    mockCopyText.mockResolvedValue(true);
    const ok = await copyPageMarkdown(p);
    expect(ok).toBe(true);
    expect(mockCopyText).toHaveBeenCalledWith(buildPageExportDocument(p));
    expect(mockToastAdd).toHaveBeenCalledWith(
      expect.objectContaining({ title: "Markdown copied", type: "success" }),
    );
  });

  it("toasts an error and returns false when the copy fails", async () => {
    mockCopyText.mockResolvedValue(false);
    const ok = await copyPageMarkdown(p);
    expect(ok).toBe(false);
    expect(mockCopyText).toHaveBeenCalledWith(buildPageExportDocument(p));
    expect(mockToastAdd).toHaveBeenCalledWith(
      expect.objectContaining({
        title: "Could not copy Markdown",
        type: "error",
      }),
    );
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
