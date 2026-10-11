// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";
import { isMermaidFence, MERMAID_CLASS } from "./markdown";
import { hydrateMermaidDiagrams, resetMermaidForTests } from "./mermaid";

type MermaidModule = typeof import("mermaid");

/** Build a fake mermaid module whose render behaves as the test needs. */
function fakeModule(
  render: (id: string, text: string) => Promise<{ svg: string }>,
): MermaidModule {
  return { default: { render } } as unknown as MermaidModule;
}

/** A detached container holding one mermaid placeholder, as markdown.ts emits. */
function placeholder(source: string): HTMLDivElement {
  const el = document.createElement("div");
  el.innerHTML =
    `<pre class="${MERMAID_CLASS}"><code class="language-mermaid">` +
    `${source}</code></pre>`;
  return el;
}

describe("isMermaidFence", () => {
  it.each([
    ["mermaid", true],
    ["MERMAID", true],
    ["Mermaid", true],
    ["mermaid ", true],
    ["  mermaid  ", true],
    ["mermaid title", true],
    ["mermaidfoo", false],
    ["js", false],
    ["", false],
    ["notmermaid", false],
  ])("isMermaidFence(%j) === %s", (info, expected) => {
    expect(isMermaidFence(info)).toBe(expected);
  });
});

describe("hydrateMermaidDiagrams", () => {
  beforeEach(() => resetMermaidForTests());

  it("replaces the code block with the rendered SVG on success", async () => {
    const render = vi.fn(
      async (_id: string, _text: string) => ({ svg: "<svg>diagram</svg>" }),
    );
    const root = placeholder("graph TD\nA--&gt;B");
    await hydrateMermaidDiagrams(root, async () => fakeModule(render));

    const pre = root.querySelector("pre")!;
    expect(render).toHaveBeenCalledTimes(1);
    // The raw source (unescaped by the DOM) reaches mermaid.
    expect(render).toHaveBeenCalledWith(
      expect.stringMatching(/^glance-mermaid-/),
      expect.stringContaining("graph TD"),
    );
    expect(pre.innerHTML).toBe("<svg>diagram</svg>");
    expect(pre.getAttribute("data-mermaid-state")).toBe("done");
    expect(pre.getAttribute("role")).toBe("img");
  });

  it("leaves the code block intact when rendering fails", async () => {
    const render = vi.fn(async () => {
      throw new Error("bad syntax");
    });
    const root = placeholder("not a diagram {{{");
    await hydrateMermaidDiagrams(root, async () => fakeModule(render));

    const pre = root.querySelector("pre")!;
    expect(pre.getAttribute("data-mermaid-state")).toBe("failed");
    // Honest degradation: the fallback code block is untouched, never blank.
    expect(pre.querySelector("code")).not.toBeNull();
    expect(pre.textContent).toContain("not a diagram {{{");
  });

  it("leaves the code block intact when the mermaid import fails", async () => {
    const root = placeholder("graph TD\nA-->B");
    await hydrateMermaidDiagrams(root, async () => {
      throw new Error("offline");
    });

    const pre = root.querySelector("pre")!;
    expect(pre.getAttribute("data-mermaid-state")).toBe("failed");
    expect(pre.querySelector("code")).not.toBeNull();
    expect(pre.textContent).toContain("graph TD");
  });

  it("skips empty diagrams without calling mermaid", async () => {
    const render = vi.fn(async () => ({ svg: "<svg/>" }));
    const root = placeholder("   ");
    await hydrateMermaidDiagrams(root, async () => fakeModule(render));

    expect(render).not.toHaveBeenCalled();
    expect(root.querySelector("pre")!.getAttribute("data-mermaid-state")).toBe(
      "failed",
    );
  });

  it("is idempotent: processed blocks are not re-rendered", async () => {
    const render = vi.fn(async () => ({ svg: "<svg/>" }));
    const load = async () => fakeModule(render);
    const root = placeholder("graph TD");

    await hydrateMermaidDiagrams(root, load);
    await hydrateMermaidDiagrams(root, load);
    expect(render).toHaveBeenCalledTimes(1);
  });

  it("renders multiple diagrams on one page", async () => {
    const render = vi.fn(async (_id: string, text: string) => ({
      svg: `<svg>${text.length}</svg>`,
    }));
    const root = document.createElement("div");
    root.appendChild(placeholder("graph TD"));
    root.appendChild(placeholder("sequenceDiagram"));

    await hydrateMermaidDiagrams(root, async () => fakeModule(render));
    expect(render).toHaveBeenCalledTimes(2);
    const states = Array.from(root.querySelectorAll("pre")).map((p) =>
      p.getAttribute("data-mermaid-state"),
    );
    expect(states).toEqual(["done", "done"]);
  });
});
