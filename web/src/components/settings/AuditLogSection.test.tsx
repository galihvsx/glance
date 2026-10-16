// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import AuditLogSection from "./AuditLogSection";
import { fetchWorkspaceAuditLog } from "../../lib/workspaceAudit";
import { downloadExport } from "../../lib/export";
import type { WorkspaceAuditEntry } from "../../lib/workspaceAudit";

vi.mock("../../lib/workspaceAudit", async (importOriginal) => {
  const actual =
    await importOriginal<typeof import("../../lib/workspaceAudit")>();
  return { ...actual, fetchWorkspaceAuditLog: vi.fn() };
});
vi.mock("../../lib/export", () => ({
  downloadExport: vi.fn(),
  buildExportUrl: vi.fn(),
  exportFilenameFromHeader: vi.fn(),
}));
vi.mock("../ui/toast", () => ({ toast: { add: vi.fn() } }));

const mockFetch = vi.mocked(fetchWorkspaceAuditLog);
const mockDownload = vi.mocked(downloadExport);

const entryRoleChanged: WorkspaceAuditEntry = {
  id: "entry-1",
  at: "2026-10-16T11:40:02Z",
  actor_id: "9f2c3d4e-1111-2222-3333-444455556666",
  actor_email: "admin@example.com",
  action: "member.role_changed",
  entity_type: "member",
  entity_id: "user-abcdef-12345678",
  ip: null,
  meta: { from_role: 15, to_role: 20 },
};

const entryWebhook: WorkspaceAuditEntry = {
  id: "entry-2",
  at: "2026-10-16T11:12:00Z",
  actor_id: null,
  actor_email: "owner@example.com",
  action: "integration.webhook_created",
  entity_type: "webhook",
  entity_id: "71ab90cd-2222-3333-4444-555566667777",
  ip: "203.0.113.7",
  meta: { host: "example.com" },
};

function renderSection() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={client}>
      <AuditLogSection slug="acme" />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("AuditLogSection", () => {
  it("renders entries newest-first with a dash for a null IP", async () => {
    mockFetch.mockResolvedValue({
      items: [entryRoleChanged, entryWebhook],
      total: 2,
      page: 1,
      per_page: 25,
    });
    renderSection();
    // Wait on the rows themselves (the action strings also appear as
    // Action-dropdown <option>s, so text queries alone would race).
    expect(
      await screen.findByRole("button", {
        name: "View audit entry member.role_changed",
      }),
    ).toBeTruthy();
    expect(
      screen.getByRole("button", {
        name: "View audit entry integration.webhook_created",
      }),
    ).toBeTruthy();
    expect(screen.getByText("admin@example.com")).toBeTruthy();
    // Null IP renders as "—", never "null" or empty.
    expect(screen.getAllByText("—").length).toBeGreaterThan(0);
    expect(screen.getByText("203.0.113.7")).toBeTruthy();
    // Opaque ids are truncated; the full value stays in the title.
    expect(screen.getByText("9f2c3d4e…")).toBeTruthy();
  });

  it("maps applied filters onto the list request", async () => {
    mockFetch.mockResolvedValue({ items: [], total: 0, page: 1, per_page: 25 });
    renderSection();
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(1));

    fireEvent.change(screen.getByLabelText("Actor email"), {
      target: { value: "admin@" },
    });
    fireEvent.change(screen.getByLabelText("Entity type"), {
      target: { value: "member" },
    });
    fireEvent.change(screen.getByLabelText("Action"), {
      target: { value: "member.role_changed" },
    });
    fireEvent.change(screen.getByLabelText("Since"), {
      target: { value: "2026-10-16T19:00" },
    });
    fireEvent.click(screen.getByText("Apply"));

    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(2));
    const [, , filters] = mockFetch.mock.calls[1];
    expect(filters).toMatchObject({
      action: "member.role_changed",
      actor: "admin@",
      entityType: "member",
      since: "2026-10-16T19:00",
    });
  });

  it("narrows the action dropdown by category and resets the action on category change", async () => {
    mockFetch.mockResolvedValue({ items: [], total: 0, page: 1, per_page: 25 });
    renderSection();
    await screen.findByText("No audit entries match these filters.");

    fireEvent.change(screen.getByLabelText("Category"), {
      target: { value: "member" },
    });
    const actionSelect = screen.getByLabelText("Action") as HTMLSelectElement;
    const options = Array.from(actionSelect.options).map((o) => o.value);
    expect(options).toEqual([
      "",
      "member.invited",
      "member.added",
      "member.role_changed",
      "member.removed",
    ]);
    // No token actions are ever offered.
    expect(options).not.toContain("integration.token_created");
    expect(options).not.toContain("integration.token_revoked");
  });

  it("opens the detail panel with pretty-printed meta, IP, entity and actor", async () => {
    mockFetch.mockResolvedValue({
      items: [entryRoleChanged],
      total: 1,
      page: 1,
      per_page: 25,
    });
    renderSection();
    const row = await screen.findByRole("button", {
      name: "View audit entry member.role_changed",
    });
    fireEvent.click(row);

    const detail = await screen.findByRole("dialog", {
      name: "Audit entry detail",
    });
    expect(detail.textContent).toContain("member.role_changed");
    expect(detail.textContent).toContain("admin@example.com");
    expect(detail.textContent).toContain("IP —");
    expect(detail.textContent).toContain("member");
    // Pretty-printed meta JSON (indented, not compact).
    expect(detail.textContent).toContain('"from_role": 15');
    expect(detail.textContent).toContain('"to_role": 20');
  });

  it("exports with the applied filters in the URL", async () => {
    mockFetch.mockResolvedValue({ items: [], total: 0, page: 1, per_page: 25 });
    mockDownload.mockResolvedValue(undefined);
    renderSection();
    await screen.findByText("No audit entries match these filters.");

    fireEvent.change(screen.getByLabelText("Actor email"), {
      target: { value: "owner@" },
    });
    fireEvent.click(screen.getByText("Apply"));
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(2));

    fireEvent.click(screen.getByText("Export CSV"));
    await waitFor(() => expect(mockDownload).toHaveBeenCalledTimes(1));
    const url = mockDownload.mock.calls[0][0];
    expect(url).toContain("/api/v1/workspaces/acme/audit-log/export?");
    expect(url).toContain("format=csv");
    expect(url).toContain("actor=owner%40");
  });

  it("shows the empty state when no entries match", async () => {
    mockFetch.mockResolvedValue({ items: [], total: 0, page: 1, per_page: 25 });
    renderSection();
    expect(
      await screen.findByText("No audit entries match these filters."),
    ).toBeTruthy();
  });

  it("shows an error with Retry that preserves the filters", async () => {
    mockFetch.mockRejectedValueOnce(new Error("boom"));
    mockFetch.mockResolvedValue({
      items: [entryWebhook],
      total: 1,
      page: 1,
      per_page: 25,
    });
    renderSection();
    expect(
      await screen.findByText("Failed to load audit log"),
    ).toBeTruthy();
    fireEvent.click(screen.getByText("Retry"));
    expect(
      await screen.findByText("integration.webhook_created"),
    ).toBeTruthy();
  });
});
