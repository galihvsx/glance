// @vitest-environment jsdom
// Component tests for the member capacity editor popover (C29T2): trigger
// label (set/unset), validation (rejects non-numeric and >999), save
// (PUTs the capacity and calls onSaved), clear (PUTs null), and the
// read-only badge when canEdit is false.

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import CapacityEditor from "./CapacityEditor";
import { api, ApiError } from "../../lib/api";

vi.mock("../../lib/api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../../lib/api")>();
  return {
    ...mod,
    api: {
      get: vi.fn(),
      post: vi.fn(),
      put: vi.fn(),
      patch: vi.fn(),
      del: vi.fn(),
    },
  };
});

const put = () => api.put as unknown as Mock;

const baseProps = {
  workspaceSlug: "acme",
  userId: "u-1",
  displayName: "Dev One",
  onSaved: vi.fn(),
};

function renderEditor(props: Partial<typeof baseProps> & { capacity?: number | null; canEdit?: boolean }) {
  return render(
    <CapacityEditor
      {...baseProps}
      capacity={null}
      canEdit={true}
      onSaved={baseProps.onSaved}
      {...props}
    />,
  );
}

beforeEach(() => {
  put().mockReset();
  baseProps.onSaved.mockReset();
});

afterEach(cleanup);

describe("CapacityEditor", () => {
  it("shows an em-dash when capacity is unset", () => {
    renderEditor({ capacity: null });
    expect(screen.getByTitle(/Edit Dev One's weekly capacity/)).toHaveTextContent("—");
  });

  it("shows the point value when capacity is set", () => {
    renderEditor({ capacity: 8 });
    expect(screen.getByTitle(/Edit Dev One's weekly capacity/)).toHaveTextContent("8 pts");
  });

  it("saves a valid capacity via PUT and calls onSaved", async () => {
    put().mockResolvedValue({ user_id: "u-1", weekly_capacity_points: 10 });
    const onSaved = vi.fn();
    renderEditor({ capacity: 8, onSaved });

    fireEvent.click(screen.getByTitle(/Edit Dev One's weekly capacity/));
    const input = await screen.findByLabelText(/Weekly capacity/);
    fireEvent.change(input, { target: { value: "10" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(put()).toHaveBeenCalledWith(
        "/api/v1/workspaces/acme/members/u-1/capacity",
        { weekly_capacity_points: 10 },
      );
    });
    expect(onSaved).toHaveBeenCalledWith(10);
  });

  it("sends null when the input is empty (clear via save)", async () => {
    put().mockResolvedValue({ user_id: "u-1", weekly_capacity_points: null });
    const onSaved = vi.fn();
    renderEditor({ capacity: 8, onSaved });

    fireEvent.click(screen.getByTitle(/Edit Dev One's weekly capacity/));
    await screen.findByLabelText(/Weekly capacity/);
    // The input is pre-filled with "8"; blank it, then save.
    const input = screen.getByLabelText(/Weekly capacity/);
    fireEvent.change(input, { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(put()).toHaveBeenCalledWith(
        "/api/v1/workspaces/acme/members/u-1/capacity",
        { weekly_capacity_points: null },
      );
    });
    expect(onSaved).toHaveBeenCalledWith(null);
  });

  it("rejects non-numeric and out-of-range input without calling the API", async () => {
    renderEditor({ capacity: null });
    fireEvent.click(screen.getByTitle(/Edit Dev One's weekly capacity/));
    const input = await screen.findByLabelText(/Weekly capacity/);

    fireEvent.change(input, { target: { value: "abc" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/whole number/);
    expect(put()).not.toHaveBeenCalled();

    fireEvent.change(input, { target: { value: "1000" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/0–999/);
    expect(put()).not.toHaveBeenCalled();
  });

  it("surfaces API errors inline", async () => {
    put().mockRejectedValue(new ApiError(400, "service: invalid capacity", "bad_request"));
    renderEditor({ capacity: null });
    fireEvent.click(screen.getByTitle(/Edit Dev One's weekly capacity/));
    const input = await screen.findByLabelText(/Weekly capacity/);
    fireEvent.change(input, { target: { value: "5" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/invalid capacity/);
  });

  it("renders a read-only badge when canEdit is false", () => {
    renderEditor({ capacity: 8, canEdit: false });
    const badge = screen.getByText("8 pts");
    expect(badge).toBeTruthy();
    expect(screen.queryByTitle(/Edit Dev One's weekly capacity/)).toBeNull();
  });
});
