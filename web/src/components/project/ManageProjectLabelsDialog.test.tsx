// ManageProjectLabelsDialog tests (C76T1): workspace project-label
// taxonomy CRUD — list, create, rename, delete (with the usage-count
// confirm), hex validation, 409 surfacing, and the guest read-only gate.
// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import ManageProjectLabelsDialog from "./ManageProjectLabelsDialog";
import { api, ApiError } from "../../lib/api";
import type { ProjectLabel } from "../../lib/types";

vi.mock("../../lib/api", () => ({
  api: { get: vi.fn(), post: vi.fn(), patch: vi.fn(), del: vi.fn() },
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
const mockPatch = vi.mocked(api.patch);
const mockDel = vi.mocked(api.del);

afterEach(() => {
  vi.clearAllMocks();
  vi.restoreAllMocks();
});

const SLUG = "acme";
const LIST_PATH = `/api/v1/workspaces/${SLUG}/project-labels`;

const pl = (id: string, name: string, color = "#3b82f6"): ProjectLabel => ({
  id,
  name,
  color,
});

function renderDialog(props?: {
  canEdit?: boolean;
  projectUsage?: Map<string, number>;
}) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  mockGet.mockImplementation((path: string) => {
    if (path === LIST_PATH)
      return Promise.resolve({
        project_labels: [pl("l1", "platform"), pl("l2", "mobile", "#ef4444")],
      });
    throw new Error(`unexpected GET ${path}`);
  });
  render(
    <QueryClientProvider client={qc}>
      <ManageProjectLabelsDialog
        slug={SLUG}
        open
        onOpenChange={() => {}}
        projectUsage={props?.projectUsage ?? new Map()}
        canEdit={props?.canEdit ?? true}
      />
    </QueryClientProvider>,
  );
}

describe("ManageProjectLabelsDialog", () => {
  it("lists the workspace project labels", async () => {
    renderDialog();
    const list = await screen.findByTestId("project-label-list");
    expect(within(list).getByText("platform")).toBeInTheDocument();
    expect(within(list).getByText("mobile")).toBeInTheDocument();
  });

  it("creates a label with name + color", async () => {
    mockPost.mockResolvedValue(pl("l3", "infra", "#10b981"));
    renderDialog();
    await screen.findByTestId("project-label-list");

    fireEvent.click(screen.getByRole("button", { name: /new label/i }));
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "infra" },
    });
    fireEvent.change(screen.getByLabelText("Color"), {
      target: { value: "#10b981" },
    });
    fireEvent.click(screen.getByRole("button", { name: /create label/i }));

    await waitFor(() => expect(mockPost).toHaveBeenCalledTimes(1));
    expect(mockPost.mock.calls[0][0]).toBe(LIST_PATH);
    expect(mockPost.mock.calls[0][1]).toEqual({
      name: "infra",
      color: "#10b981",
    });
  });

  it("blocks creation with an invalid hex color", async () => {
    renderDialog();
    await screen.findByTestId("project-label-list");

    fireEvent.click(screen.getByRole("button", { name: /new label/i }));
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "infra" },
    });
    fireEvent.change(screen.getByLabelText("Color"), {
      target: { value: "not-a-color" },
    });
    expect(
      screen.getByRole("button", { name: /create label/i }),
    ).toBeDisabled();
    expect(mockPost).not.toHaveBeenCalled();
  });

  it("renames and recolors a label via PATCH", async () => {
    mockPatch.mockResolvedValue(pl("l1", "platform-core", "#8b5cf6"));
    renderDialog();
    await screen.findByTestId("project-label-list");

    fireEvent.click(screen.getByRole("button", { name: "Rename platform" }));
    const nameInput = screen.getByLabelText("Name");
    fireEvent.change(nameInput, { target: { value: "platform-core" } });
    fireEvent.change(screen.getByLabelText("Color"), {
      target: { value: "#8b5cf6" },
    });
    fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

    await waitFor(() => expect(mockPatch).toHaveBeenCalledTimes(1));
    expect(mockPatch.mock.calls[0][0]).toBe(`${LIST_PATH}/l1`);
    expect(mockPatch.mock.calls[0][1]).toEqual({
      name: "platform-core",
      color: "#8b5cf6",
    });
  });

  it("deletes behind a confirm that shows how many projects carry the label", async () => {
    const confirm = vi
      .spyOn(window, "confirm")
      .mockReturnValue(true);
    mockDel.mockResolvedValue(undefined);
    renderDialog({
      projectUsage: new Map([["l1", 3]]),
    });
    await screen.findByTestId("project-label-list");

    fireEvent.click(screen.getByRole("button", { name: "Delete platform" }));

    expect(confirm).toHaveBeenCalledTimes(1);
    const msg = confirm.mock.calls[0][0] as string;
    expect(msg).toContain('"platform"');
    expect(msg).toContain("3 projects");
    await waitFor(() => expect(mockDel).toHaveBeenCalledTimes(1));
    expect(mockDel.mock.calls[0][0]).toBe(`${LIST_PATH}/l1`);
  });

  it("does not delete when the confirm is dismissed", async () => {
    vi.spyOn(window, "confirm").mockReturnValue(false);
    renderDialog();
    await screen.findByTestId("project-label-list");

    fireEvent.click(screen.getByRole("button", { name: "Delete platform" }));
    expect(mockDel).not.toHaveBeenCalled();
  });

  it("surfaces a 409 duplicate-name error", async () => {
    mockPost.mockRejectedValue(new ApiError(409, "Label name already exists"));
    renderDialog();
    await screen.findByTestId("project-label-list");

    fireEvent.click(screen.getByRole("button", { name: /new label/i }));
    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "platform" },
    });
    fireEvent.click(screen.getByRole("button", { name: /create label/i }));

    await screen.findByText("Label name already exists");
  });

  it("is read-only for guests: no create, rename, or delete affordances", async () => {
    renderDialog({ canEdit: false });
    await screen.findByTestId("project-label-list");

    expect(
      screen.queryByRole("button", { name: /new label/i }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Rename platform" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Delete platform" }),
    ).not.toBeInTheDocument();
  });
});
