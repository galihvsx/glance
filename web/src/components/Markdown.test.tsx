// @vitest-environment jsdom
// Component tests for the Markdown renderer (C18T5): mermaid fences become
// diagrams via a mocked mermaid module, failures keep the code block.
import { render, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { Markdown } from "./Markdown";

vi.mock("mermaid", () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn(async (_id: string, text: string) => ({
      svg: `<svg data-mocked="true">${text.length}</svg>`,
    })),
  },
}));

describe("Markdown (C18T5)", () => {
  it("hydrates mermaid fences into diagrams", async () => {
    const { container } = render(
      <Markdown markdown={"```mermaid\ngraph TD\nA-->B\n```"} />,
    );
    // Fallback code block is present before hydration completes.
    expect(container.querySelector("pre.glance-mermaid code")).not.toBeNull();

    await waitFor(() => {
      expect(
        container.querySelector('pre.glance-mermaid svg[data-mocked="true"]'),
      ).not.toBeNull();
    });
    expect(
      container.querySelector("pre.glance-mermaid")!.getAttribute(
        "data-mermaid-state",
      ),
    ).toBe("done");
  });

  it("keeps the code block when mermaid render fails", async () => {
    const mermaid = await import("mermaid");
    vi.mocked(mermaid.default.render).mockRejectedValueOnce(
      new Error("bad syntax"),
    );

    const { container } = render(
      <Markdown markdown={"```mermaid\n{{{\n```"} />,
    );
    await waitFor(() => {
      expect(
        container.querySelector("pre.glance-mermaid")!.getAttribute(
          "data-mermaid-state",
        ),
      ).toBe("failed");
    });
    // Honest degradation: code fallback untouched, never a blank hole.
    expect(container.querySelector("pre.glance-mermaid code")).not.toBeNull();
    expect(
      container.querySelector("pre.glance-mermaid")!.textContent,
    ).toContain("{{{");
  });

  it("renders non-mermaid markdown unchanged", () => {
    const { container } = render(
      <Markdown markdown={"# Hi\n\n```js\nx();\n```"} className="wiki-content" />,
    );
    expect(container.querySelector("h1")).toHaveTextContent("Hi");
    expect(container.querySelector("pre.glance-mermaid")).toBeNull();
    expect(container.firstElementChild!.className).toContain("wiki-content");
  });
});
