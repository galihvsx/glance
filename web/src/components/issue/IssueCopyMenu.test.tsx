// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import IssueCopyMenu from "./IssueCopyMenu";
import { copyText } from "../../lib/clipboard";
import { toast } from "../ui/toast";
import type { Issue } from "../../lib/types";

// Clipboard and toast are mocked: these tests cover the menu's wiring
// (which value each item copies, toast confirmations), not the browser
// clipboard itself.
vi.mock("../../lib/clipboard", () => ({ copyText: vi.fn() }));
vi.mock("../ui/toast", () => ({ toast: { add: vi.fn() } }));

const mockCopyText = vi.mocked(copyText);
const mockToastAdd = vi.mocked(toast.add);

const issue = {
  display_id: "GLC-123",
  name: "Fix login bug",
} as Issue;

function renderMenu() {
  render(
    <IssueCopyMenu
      slug="acme"
      identifier="GLC"
      uuid="uuid-1"
      issue={issue}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Copy" }));
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("IssueCopyMenu", () => {
  it("copies the branch name and toasts on success", () => {
    mockCopyText.mockResolvedValue(true);
    renderMenu();
    fireEvent.click(screen.getByText("Copy branch name"));
    expect(mockCopyText).toHaveBeenCalledWith(
      "glance/glc-123-fix-login-bug",
    );
    return vi.waitFor(() => {
      expect(mockToastAdd).toHaveBeenCalledWith(
        expect.objectContaining({
          title: "Branch name copied",
          type: "success",
        }),
      );
    });
  });

  it("copies the full canonical issue URL", () => {
    mockCopyText.mockResolvedValue(true);
    renderMenu();
    fireEvent.click(screen.getByText("Copy issue URL"));
    expect(mockCopyText).toHaveBeenCalledWith(
      "http://localhost:3000/w/acme/p/GLC/i/uuid-1",
    );
    return vi.waitFor(() => {
      expect(mockToastAdd).toHaveBeenCalledWith(
        expect.objectContaining({ title: "Issue URL copied" }),
      );
    });
  });

  it("copies the issue ID", () => {
    mockCopyText.mockResolvedValue(true);
    renderMenu();
    fireEvent.click(screen.getByText("Copy ID"));
    expect(mockCopyText).toHaveBeenCalledWith("GLC-123");
    return vi.waitFor(() => {
      expect(mockToastAdd).toHaveBeenCalledWith(
        expect.objectContaining({ title: "Issue ID copied" }),
      );
    });
  });

  it("toasts an error when the copy fails", () => {
    mockCopyText.mockResolvedValue(false);
    renderMenu();
    fireEvent.click(screen.getByText("Copy ID"));
    return vi.waitFor(() => {
      expect(mockToastAdd).toHaveBeenCalledWith(
        expect.objectContaining({ type: "error" }),
      );
    });
  });
});
