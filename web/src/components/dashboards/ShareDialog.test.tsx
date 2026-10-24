// @vitest-environment jsdom
// C83T1: ShareDialog — owner-only share management: member search
// excludes the dashboard owner and guests (role < 15), staged edits
// commit as ONE batch PUT on Save, Cancel discards, Remove drops the
// grant from the staged list.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import ShareDialog from "./ShareDialog";
import type { WorkspaceMember } from "../../lib/types";
import type { DashboardShare } from "../../lib/dashboards";

const SLUG = "acme";
const DASH = "d1";
const OWNER = "u-owner";

const members: WorkspaceMember[] = [
  { id: OWNER, name: "Olivia Owner", email: "owner@acme.io", role: 20 },
  { id: "u-ada", name: "Ada", email: "ada@acme.io", role: 15 },
  { id: "u-ben", name: "Ben", email: "ben@acme.io", role: 15 },
  { id: "u-gus", name: "Gus Guest", email: "gus@acme.io", role: 5 },
  { id: "u-vic", name: "Vic Viewer", email: "vic@acme.io", role: 10 },
];

const existingShares: DashboardShare[] = [
  {
    member_id: "u-ben",
    member_name: "Ben",
    access: "view",
    created_at: "2026-10-20T00:00:00Z",
  },
];

const mockFetchShares = vi.hoisted(() =>
  vi.fn(() => Promise.resolve(existingShares)),
);
const mockReplaceShares = vi.hoisted(() =>
  vi.fn((_slug: string, _id: string, shares: unknown) =>
    Promise.resolve(shares),
  ),
);

vi.mock("../../lib/api", () => ({
  api: {
    get: vi.fn((path: string): Promise<unknown> => {
      if (path === `/api/v1/workspaces/${SLUG}/members`)
        return Promise.resolve({ members });
      throw new Error(`unexpected GET ${path}`);
    }),
  },
  ApiError: class ApiError extends Error {
    status: number;
    body: string;
    constructor(status: number, message: string) {
      super(message);
      this.status = status;
      this.body = message;
    }
  },
}));

vi.mock("../../lib/dashboards", async (importOriginal) => {
  const mod =
    await importOriginal<typeof import("../../lib/dashboards")>();
  return {
    ...mod,
    fetchDashboardShares: mockFetchShares,
    replaceDashboardShares: mockReplaceShares,
  };
});

afterEach(() => {
  cleanup();
  mockFetchShares.mockClear();
  mockReplaceShares.mockClear();
});

function renderDialog(onOpenChange = vi.fn()) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={qc}>
      <ShareDialog
        open
        onOpenChange={onOpenChange}
        slug={SLUG}
        dashboardId={DASH}
        dashboardName="Sprint health"
        ownerId={OWNER}
      />
    </QueryClientProvider>,
  );
  return { onOpenChange };
}

describe("ShareDialog", () => {
  it("excludes the owner and guests from member search results", async () => {
    renderDialog();
    // Ada (member) is addable; the existing share (Ben) is listed.
    expect(await screen.findByText("Ada")).toBeInTheDocument();
    expect(screen.getByText("Who has access")).toBeInTheDocument();
    expect(screen.queryByText("Olivia Owner")).not.toBeInTheDocument();
    expect(screen.queryByText("Gus Guest")).not.toBeInTheDocument();
    expect(screen.queryByText("Vic Viewer")).not.toBeInTheDocument();
    // Ben is already shared, so he is not offered as a candidate.
    expect(
      screen.queryByRole("button", { name: "Add Ben" }),
    ).not.toBeInTheDocument();
  });

  it("commits add + access change as a single batch PUT on Save", async () => {
    renderDialog();
    await screen.findByText("Ada");
    // Add Ada with Edit access.
    fireEvent.change(screen.getByLabelText("Access for Ada"), {
      target: { value: "edit" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Add Ada" }));
    // Promote Ben's existing grant to Edit.
    fireEvent.change(screen.getByLabelText("Access for Ben"), {
      target: { value: "edit" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(mockReplaceShares).toHaveBeenCalledTimes(1),
    );
    expect(mockReplaceShares).toHaveBeenCalledWith(SLUG, DASH, [
      { member_id: "u-ben", access: "edit" },
      { member_id: "u-ada", access: "edit" },
    ]);
  });

  it("removes a grant from the staged list before saving", async () => {
    renderDialog();
    await screen.findByText("Ada");
    fireEvent.click(screen.getByRole("button", { name: "Remove Ben" }));
    expect(screen.getByText("Not shared with anyone yet.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(mockReplaceShares).toHaveBeenCalledTimes(1),
    );
    expect(mockReplaceShares).toHaveBeenCalledWith(SLUG, DASH, []);
  });

  it("discards staged changes on Cancel (no PUT)", async () => {
    const { onOpenChange } = renderDialog();
    await screen.findByText("Ada");
    fireEvent.click(screen.getByRole("button", { name: "Add Ada" }));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(mockReplaceShares).not.toHaveBeenCalled();
  });
});
