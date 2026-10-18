// @vitest-environment jsdom
// Component tests for the WorkflowSection (C68T1): unrestricted banner,
// per-state allowed-next editor, save flow with the transitions payload,
// restricted-mode "no moves out" hint, and inline 403/400 error surfacing.

import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi, type Mock } from "vitest";
import WorkflowSection from "./WorkflowSection";
import { api, ApiError } from "../../lib/api";

vi.mock("../../lib/api", async (importOriginal) => {
  const mod = await importOriginal<typeof import("../../lib/api")>();
  return {
    ...mod,
    api: { ...mod.api, get: vi.fn(), put: vi.fn() },
  };
});
vi.mock("../ui/toast", () => ({ toast: { add: vi.fn() } }));

const getMock = api.get as unknown as Mock;
const putMock = api.put as unknown as Mock;

const STATES = [
  { id: "s1", project_id: "p1", name: "Backlog", group: "backlog", color: "#6b7280", sequence: 0, created_at: "", updated_at: "" },
  { id: "s2", project_id: "p1", name: "In Progress", group: "started", color: "#3b82f6", sequence: 1, created_at: "", updated_at: "" },
  { id: "s3", project_id: "p1", name: "Done", group: "completed", color: "#22c55e", sequence: 2, created_at: "", updated_at: "" },
];

const UNRESTRICTED = { states: STATES, transitions: [] };
const RESTRICTED = {
  states: STATES,
  transitions: [{ from_state_id: "s1", to_state_id: "s2" }],
};

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  getMock.mockReset();
  putMock.mockReset();
});

function renderSection() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <WorkflowSection slug="acme" identifier="EXAMP" canEdit />
    </QueryClientProvider>,
  );
}

describe("WorkflowSection", () => {
  it("shows the unrestricted banner when the project has no rules", async () => {
    getMock.mockResolvedValue(UNRESTRICTED);
    renderSection();
    const banner = await screen.findByRole("alert");
    expect(within(banner).getByText(/unrestricted/i)).toBeInTheDocument();
    expect(
      within(banner).getByText(/any state can move to any other state/i),
    ).toBeInTheDocument();
  });

  it("sends the edited transitions payload on save", async () => {
    getMock.mockResolvedValue(UNRESTRICTED);
    putMock.mockResolvedValue(RESTRICTED);
    renderSection();
    const row = await screen.findByTestId("workflow-from-s1");

    // Allow Backlog → In Progress: the per-state row's checkbox.
    const checkbox = row.querySelector('input[type="checkbox"]') as HTMLInputElement;
    fireEvent.click(checkbox);
    fireEvent.click(screen.getByRole("button", { name: /save workflow/i }));

    await waitFor(() => expect(putMock).toHaveBeenCalledTimes(1));
    const [path, body] = putMock.mock.calls[0] as [string, { transitions: unknown[] }];
    expect(path).toBe("/api/v1/workspaces/acme/projects/EXAMP/workflow");
    expect(body).toEqual({ transitions: [{ from_state_id: "s1", to_state_id: "s2" }] });
  });

  it("hints that a state with no outgoing rules allows no moves when restricted", async () => {
    getMock.mockResolvedValue(RESTRICTED);
    renderSection();
    const doneRow = await screen.findByTestId("workflow-from-s3");
    // Done has no outgoing rules while the project is restricted.
    expect(doneRow).toHaveTextContent(/no moves out allowed/i);
  });

  it("surfaces a 403 error inline without crashing", async () => {
    getMock.mockResolvedValue(UNRESTRICTED);
    putMock.mockRejectedValue(new ApiError(403, "forbidden", "forbidden"));
    renderSection();
    const firstRow = await screen.findByTestId("workflow-from-s1");

    fireEvent.click(firstRow.querySelector('input[type="checkbox"]') as HTMLInputElement);
    const saveBtn = screen.getByRole("button", { name: /save workflow/i });
    fireEvent.click(saveBtn);
    await waitFor(() => expect(putMock).toHaveBeenCalled());

    const alerts = await screen.findAllByRole("alert");
    expect(
      alerts.some((a) => /forbidden|permission/i.test(a.textContent ?? "")),
    ).toBe(true);
  });
});
