// ProjectLabelAssignment tests (C76T1): per-project assignment of the
// workspace project-label taxonomy — the LabelPicker-style multi-select,
// guest read-only, and assign/unassign round-trips.
// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import ProjectLabelAssignment from "./ProjectLabelAssignment";
import { api, ApiError } from "../../lib/api";
import type { Project, ProjectLabel } from "../../lib/types";

vi.mock("../../lib/api", () => ({
  api: { get: vi.fn(), post: vi.fn(), del: vi.fn() },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(status: number, message: string) {
      super(message);
      this.name = "ApiError";
      this.status = status;
    }
  },
}));

const mockGet = vi.mocked(api.get);
const mockPost = vi.mocked(api.post);
const mockDel = vi.mocked(api.del);

afterEach(() => {
  vi.clearAllMocks();
});

const SLUG = "acme";
const IDENT = "ENG";
const DETAIL_PATH = `/api/v1/workspaces/${SLUG}/projects/${IDENT}`;
const LABELS_PATH = `/api/v1/workspaces/${SLUG}/project-labels`;

const pl = (id: string, name: string, color = "#3b82f6"): ProjectLabel => ({
  id,
  name,
  color,
});

const project: Project = {
  id: "p-eng",
  workspace_id: "w1",
  identifier: IDENT,
  name: "Engineering",
  description: "",
  labels: [pl("l1", "platform")],
  created_at: "",
  updated_at: "",
  parallel_cycles: false,
};

function renderAssignment(canEdit: boolean) {
  const qc = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
  mockGet.mockImplementation((path: string) => {
    if (path === DETAIL_PATH) return Promise.resolve(project);
    if (path === LABELS_PATH)
      return Promise.resolve({
        project_labels: [pl("l1", "platform"), pl("l2", "mobile", "#ef4444")],
      });
    throw new Error(`unexpected GET ${path}`);
  });
  render(
    <QueryClientProvider client={qc}>
      <ProjectLabelAssignment slug={SLUG} identifier={IDENT} canEdit={canEdit} />
    </QueryClientProvider>,
  );
}

describe("ProjectLabelAssignment", () => {
  it("shows the assigned labels as chips", async () => {
    renderAssignment(true);
    expect(await screen.findByTitle("platform")).toBeInTheDocument();
  });

  it("assigns a label through the picker (POST)", async () => {
    mockPost.mockResolvedValue(undefined);
    renderAssignment(true);
    await screen.findByTitle("platform");

    fireEvent.click(screen.getByRole("button", { name: /1 label/i }));
    await screen.findByText("mobile");
    fireEvent.click(screen.getByText("mobile"));

    await waitFor(() => expect(mockPost).toHaveBeenCalledTimes(1));
    expect(mockPost.mock.calls[0][0]).toBe(`${DETAIL_PATH}/project-labels/l2`);
  });

  it("unassigns a label through the picker (DELETE)", async () => {
    mockDel.mockResolvedValue(undefined);
    renderAssignment(true);
    await screen.findByTitle("platform");

    fireEvent.click(screen.getByRole("button", { name: /1 label/i }));
    const popover = await screen.findByRole("dialog");
    fireEvent.click(within(popover).getByText("platform"));

    await waitFor(() => expect(mockDel).toHaveBeenCalledTimes(1));
    expect(mockDel.mock.calls[0][0]).toBe(`${DETAIL_PATH}/project-labels/l1`);
  });

  it("is read-only for guests: chips but no assign UI", async () => {
    renderAssignment(false);
    expect(await screen.findByTitle("platform")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /assign labels|label/i }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText(/guest here — labels are read-only/i),
    ).toBeInTheDocument();
  });

  it("surfaces mutation errors", async () => {
    mockPost.mockRejectedValue(new ApiError(403, "Guests cannot assign labels"));
    renderAssignment(true);
    await screen.findByTitle("platform");

    fireEvent.click(screen.getByRole("button", { name: /1 label/i }));
    await screen.findByText("mobile");
    fireEvent.click(screen.getByText("mobile"));

    await screen.findByText("Guests cannot assign labels");
  });
});
