// @vitest-environment jsdom
// C13T1: the digest schedule controls gain a timezone input (shortlist
// datalist + free-text IANA entry). The hour is evaluated in the
// selected timezone and the copy says so.
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DigestScheduleControls } from "./Notifications";
import { fetchDigestSchedule, setDigestSchedule } from "../lib/notifications";
import type { DigestSchedule } from "../lib/notifications";

vi.mock("../lib/notifications", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("../lib/notifications")>();
  return {
    ...actual,
    fetchDigestSchedule: vi.fn(),
    setDigestSchedule: vi.fn(),
  };
});

const mockFetchSchedule = vi.mocked(fetchDigestSchedule);
const mockSetSchedule = vi.mocked(setDigestSchedule);

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

function setup(sched: DigestSchedule = { frequency: "daily", hour: 8, tz: "UTC", server_tz: "UTC" }) {
  mockFetchSchedule.mockResolvedValue(sched);
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <DigestScheduleControls enabled />
    </QueryClientProvider>,
  );
  return { client };
}

describe("DigestScheduleControls timezone", () => {
  it("renders a timezone field showing the current zone", async () => {
    setup({ frequency: "daily", hour: 8, tz: "Asia/Makassar", server_tz: "UTC" });
    const input = await screen.findByLabelText(/time ?zone/i);
    await waitFor(() => expect(input).toHaveValue("Asia/Makassar"));
  });

  it("offers a datalist of IANA zones for the free-text entry", async () => {
    setup();
    await screen.findByLabelText(/time ?zone/i);
    const options = document.querySelectorAll("datalist#digest-tz-list option");
    const values = Array.from(options).map((o) => o.getAttribute("value"));
    for (const zone of ["UTC", "Asia/Jakarta", "Asia/Makassar", "America/New_York"]) {
      expect(values).toContain(zone);
    }
    // Free-text: the input is not a <select>.
    expect(document.querySelector("datalist#digest-tz-list")).not.toBeNull();
  });

  it("saves the new timezone on blur", async () => {
    setup();
    const input = (await screen.findByLabelText(/time ?zone/i)) as HTMLInputElement;
    mockSetSchedule.mockResolvedValue({
      frequency: "daily",
      hour: 8,
      tz: "Asia/Jakarta",
      server_tz: "UTC",
    });
    fireEvent.change(input, { target: { value: "Asia/Jakarta" } });
    fireEvent.blur(input);
    await waitFor(() => {
      expect(mockSetSchedule).toHaveBeenCalledWith("daily", 8, "Asia/Jakarta");
    });
  });

  it("states that the digest hour is in the selected timezone", async () => {
    setup({ frequency: "daily", hour: 8, tz: "Asia/Jakarta", server_tz: "UTC" });
    await screen.findByLabelText(/time ?zone/i);
    // JSX splits the copy across text nodes — match on full textContent.
    const copy = await screen.findByText(
      (_content, el) =>
        el?.tagName === "SPAN" &&
        el.textContent === "Digest sends after 08:00 in Asia/Jakarta.",
    );
    expect(copy).toBeInTheDocument();
  });

  it("rolls back and shows the server message when the timezone is invalid", async () => {
    setup({ frequency: "daily", hour: 8, tz: "UTC", server_tz: "UTC" });
    const input = (await screen.findByLabelText(/time ?zone/i)) as HTMLInputElement;
    mockSetSchedule.mockRejectedValue(new Error("digest timezone must be a valid IANA timezone name"));
    fireEvent.change(input, { target: { value: "Not/AZone" } });
    fireEvent.blur(input);
    await waitFor(() => {
      expect(
        screen.getByText(/digest timezone must be a valid IANA timezone name/i),
      ).toBeInTheDocument();
    });
    // Rolled back to the previous value.
    expect(input).toHaveValue("UTC");
  });
});
