import { describe, expect, it } from "vitest";
import {
  buildBranchName,
  MAX_SLUG_CHARS,
  slugifyTitle,
} from "./branchName";

describe("slugifyTitle", () => {
  it("lowercases and dash-joins words", () => {
    expect(slugifyTitle("Short Slug")).toBe("short-slug");
    expect(slugifyTitle("Fix login bug")).toBe("fix-login-bug");
  });

  it("collapses runs of non-alphanumerics and trims dashes", () => {
    expect(slugifyTitle("  Fix: login   bug!! ")).toBe("fix-login-bug");
    expect(slugifyTitle("a_b_c")).toBe("a-b-c");
    expect(slugifyTitle("--hello--")).toBe("hello");
  });

  it("strips non-ASCII characters", () => {
    // Diacritics are stripped, not transliterated.
    expect(slugifyTitle("café")).toBe("caf");
    // CJK and emoji vanish entirely.
    expect(slugifyTitle("Perbaiki 日本語 bug 🐛")).toBe("perbaiki-bug");
  });

  it("truncates to MAX_SLUG_CHARS and trims a trailing dash", () => {
    const long = `a${"b".repeat(60)}`;
    const slug = slugifyTitle(long);
    expect(slug.length).toBeLessThanOrEqual(MAX_SLUG_CHARS);
    expect(slug).toBe("a" + "b".repeat(MAX_SLUG_CHARS - 1));
    // Truncation landing mid-separator: "word-…" must not end with "-".
    expect(slugifyTitle(`${"x".repeat(39)}-tail`)).toBe("x".repeat(39));
  });

  it("returns empty for empty or symbol-only titles", () => {
    expect(slugifyTitle("")).toBe("");
    expect(slugifyTitle("!!!")).toBe("");
    expect(slugifyTitle("日本語")).toBe("");
  });
});

describe("buildBranchName", () => {
  it("builds glance/<id-lowercased>-<slug>", () => {
    expect(buildBranchName("GLC-123", "Short Slug")).toBe(
      "glance/glc-123-short-slug",
    );
    expect(buildBranchName("GLA-7", "Fix Login Bug")).toBe(
      "glance/gla-7-fix-login-bug",
    );
  });

  it("omits the slug separator when the title is empty", () => {
    expect(buildBranchName("GLC-1", "")).toBe("glance/glc-1");
    expect(buildBranchName("GLC-1", "日本語")).toBe("glance/glc-1");
  });

  it("keeps the slug within the limit", () => {
    const name = buildBranchName(
      "GLC-99",
      "This is a very long issue title that definitely exceeds the forty character slug limit",
    );
    const slug = name.replace("glance/glc-99-", "");
    expect(slug.length).toBeLessThanOrEqual(MAX_SLUG_CHARS);
    expect(name.endsWith("-")).toBe(false);
  });
});
