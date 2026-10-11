import { describe, expect, it } from "vitest";
import { MERMAID_CLASS, renderMarkdown } from "./markdown";

describe("renderMarkdown mermaid fences (C18T5)", () => {
  it("emits a hydratable placeholder for ```mermaid fences", () => {
    const html = renderMarkdown("```mermaid\ngraph TD\nA-->B\n```");
    expect(html).toContain(`<pre class="${MERMAID_CLASS}">`);
    expect(html).toContain("graph TD");
    // Source stays readable (HTML-escaped) as the no-JS/failure fallback.
    expect(html).toContain("A--&gt;B");
  });

  it("escapes hostile content inside mermaid fences", () => {
    const html = renderMarkdown('```mermaid\n<script>alert("x")</script>\n```');
    expect(html).toContain(MERMAID_CLASS);
    expect(html).not.toContain("<script>");
    expect(html).toContain("&lt;script&gt;");
  });

  it("matches the fence info case-insensitively", () => {
    expect(renderMarkdown("```MERMAID\ngraph TD\n```")).toContain(
      MERMAID_CLASS,
    );
    expect(renderMarkdown("```Mermaid \ngraph TD\n```")).toContain(
      MERMAID_CLASS,
    );
  });

  it("does not treat other info strings as mermaid", () => {
    for (const info of ["mermaidfoo", "js", "python", ""]) {
      const html = renderMarkdown(`\`\`\`${info}\nconst x = 1;\n\`\`\``);
      expect(html).not.toContain(MERMAID_CLASS);
      expect(html).toContain("<pre><code>");
    }
  });

  it("handles an unclosed mermaid fence without dropping content", () => {
    const html = renderMarkdown("```mermaid\ngraph TD\nA-->B");
    expect(html).toContain(MERMAID_CLASS);
    expect(html).toContain("graph TD");
  });

  it("renders mermaid fences alongside other blocks", () => {
    const html = renderMarkdown(
      "# Doc\n\n```mermaid\ngraph TD\nA-->B\n```\n\n```go\nfmt.Println()\n```",
    );
    expect(html).toContain("<h1>Doc</h1>");
    expect(html).toContain(MERMAID_CLASS);
    expect(html).toContain("<pre><code>fmt.Println()</code></pre>");
  });
});
