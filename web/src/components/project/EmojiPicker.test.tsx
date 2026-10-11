// @vitest-environment jsdom
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import EmojiPicker from "./EmojiPicker";
import { EMOJI_LIST, isSingleEmoji } from "../../lib/emoji";

function openPicker() {
  fireEvent.click(screen.getByRole("button", { name: "Choose project icon" }));
}

describe("EmojiPicker", () => {
  it("every curated emoji passes the single-emoji contract", () => {
    const bad = EMOJI_LIST.filter((e) => !isSingleEmoji(e.emoji));
    expect(bad).toEqual([]);
  });

  it("shows the placeholder affordance when unset", () => {
    render(<EmojiPicker value={null} onChange={() => {}} />);
    expect(
      screen.getByRole("button", { name: "Choose project icon" }),
    ).toBeInTheDocument();
  });

  it("opens the emoji grid on trigger click", () => {
    render(<EmojiPicker value={null} onChange={() => {}} />);
    openPicker();
    expect(
      screen.getByRole("listbox", { name: "Emoji choices" }),
    ).toBeInTheDocument();
  });

  it("picks an emoji and reports it via onChange", () => {
    const onChange = vi.fn();
    render(<EmojiPicker value={null} onChange={onChange} />);
    openPicker();
    fireEvent.click(screen.getByRole("option", { name: "rocket" }));
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith("🚀");
  });

  it("renders the current icon on the trigger", () => {
    render(<EmojiPicker value="🎯" onChange={() => {}} />);
    expect(
      screen.getByRole("button", { name: "Choose project icon" }),
    ).toHaveTextContent("🎯");
  });

  it("clears via Remove icon", () => {
    const onChange = vi.fn();
    render(<EmojiPicker value="🎯" onChange={onChange} />);
    openPicker();
    fireEvent.click(screen.getByRole("button", { name: "Remove icon" }));
    expect(onChange).toHaveBeenCalledWith(null);
  });

  it("does not offer Remove icon when unset", () => {
    render(<EmojiPicker value={null} onChange={() => {}} />);
    openPicker();
    expect(
      screen.queryByRole("button", { name: "Remove icon" }),
    ).toBeNull();
  });

  it("filters the grid by search query", () => {
    render(<EmojiPicker value={null} onChange={() => {}} />);
    openPicker();
    fireEvent.change(screen.getByLabelText("Search emoji"), {
      target: { value: "rocket" },
    });
    expect(screen.getByRole("option", { name: "rocket" })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "trophy" })).toBeNull();
  });

  it("shows an empty state for unmatched search", () => {
    render(<EmojiPicker value={null} onChange={() => {}} />);
    openPicker();
    fireEvent.change(screen.getByLabelText("Search emoji"), {
      target: { value: "zzz-no-such-emoji" },
    });
    expect(screen.getByText("No emoji match.")).toBeInTheDocument();
  });
});

describe("isSingleEmoji (client mirror of the server contract)", () => {
  it.each([
    ["🚀", true],
    ["☀️", true],
    ["👍🏽", true],
    ["🇺🇸", true],
    ["1️⃣", true],
    ["⚙️", true],
    ["©️", true],
  ])("%s is a single emoji", (input, expected) => {
    expect(isSingleEmoji(input)).toBe(expected);
  });

  it.each([
    ["", false],
    ["ENG", false],
    ["🚀🚀", false],
    ["👨‍👩‍👧", false],
    ["not-an-emoji", false],
  ])("%s is rejected", (input, expected) => {
    expect(isSingleEmoji(input)).toBe(expected);
  });
});
