// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import AutomationsSection from "./AutomationsSection";
import {
  fetchAutomationRules,
  createAutomationRule,
  updateAutomationRule,
  deleteAutomationRule,
  fetchAutomationRuns,
} from "../../lib/automations";
import { fetchStates, fetchLabels } from "../../lib/taxonomy";
import { api } from "../../lib/api";
import { toast } from "../ui/toast";
import type { AutomationRule, AutomationRun } from "../../lib/automations";

vi.mock("../../lib/automations", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/automations")>();
  return {
    ...actual,
    fetchAutomationRules: vi.fn(),
    createAutomationRule: vi.fn(),
    updateAutomationRule: vi.fn(),
    deleteAutomationRule: vi.fn(),
    fetchAutomationRuns: vi.fn(),
  };
});
vi.mock("../../lib/taxonomy", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/taxonomy")>();
  return { ...actual, fetchStates: vi.fn(), fetchLabels: vi.fn() };
});
vi.mock("../../lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/api")>();
  return { ...actual, api: { get: vi.fn(), post: vi.fn(), patch: vi.fn(), del: vi.fn() } };
});
vi.mock("../ui/toast", () => ({ toast: { add: vi.fn() } }));

const mockFetchRules = vi.mocked(fetchAutomationRules);
const mockFetchRuns = vi.mocked(fetchAutomationRuns);
const mockCreate = vi.mocked(createAutomationRule);
const mockUpdate = vi.mocked(updateAutomationRule);
const mockDelete = vi.mocked(deleteAutomationRule);
const mockFetchStates = vi.mocked(fetchStates);
const mockFetchLabels = vi.mocked(fetchLabels);
const mockApiGet = vi.mocked(api.get);
const mockToastAdd = vi.mocked(toast.add);

const run: AutomationRun = {
  id: "run-1",
  rule_id: "rule-1",
  rule_name: "Ping on done",
  issue_id: "issue-9",
  issue_display_id: "ENG-42",
  trigger_type: "issue.state_changed",
  fired_at: new Date(Date.now() - 5 * 60 * 1000).toISOString(),
  actions: [
    { type: "assign", ok: true },
    { type: "add_comment", ok: false, error: "boom" },
  ],
};

const rule: AutomationRule = {
  id: "rule-1",
  project_id: "p1",
  name: "Assign QA on start",
  trigger: { type: "issue.state_changed", from_states: null, to_states: ["state-started"] },
  actions: [{ type: "assign", user_id: "user-2" }],
  enabled: true,
  created_by: "user-1",
  created_at: "2026-10-10T00:00:00Z",
  updated_at: "2026-10-10T00:00:00Z",
};

function setup(canEdit = true) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  mockFetchRules.mockResolvedValue([rule]);
  mockFetchRuns.mockResolvedValue([run]);
  mockFetchStates.mockResolvedValue([
    { id: "state-backlog", project_id: "p1", name: "Backlog", group: "backlog", color: "#fff", sequence: 1 },
    { id: "state-started", project_id: "p1", name: "In progress", group: "started", color: "#fff", sequence: 2 },
  ]);
  mockFetchLabels.mockResolvedValue([{ id: "label-1", name: "bug", color: "#ef4444" }]);
  mockApiGet.mockResolvedValue({
    members: [{ id: "user-2", name: "Bob", email: "bob@example.com", role: 15 }],
  });
  render(
    <MemoryRouter>
      <QueryClientProvider client={client}>
        <AutomationsSection slug="acme" identifier="ENG" canEdit={canEdit} />
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

describe("AutomationsSection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });
  afterEach(() => {
    cleanup();
  });

  it("renders the rule list with trigger and action summaries", async () => {
    setup();
    expect(await screen.findByText("Assign QA on start")).toBeTruthy();
    expect(await screen.findByText(/When issue moves from any state/)).toBeTruthy();
    expect(await screen.findByText(/Assign to Bob/)).toBeTruthy();
  });

  it("toggles a rule off and shows an honest toast on failure", async () => {
    setup();
    const toggle = await screen.findByRole("switch", { name: /Enable rule/ });
    mockUpdate.mockRejectedValueOnce(new Error("boom"));
    fireEvent.click(toggle);
    await waitFor(() => expect(mockUpdate).toHaveBeenCalled());
    expect(mockUpdate.mock.calls[0][3]).toEqual({ enabled: false });
    await waitFor(() =>
      expect(mockToastAdd).toHaveBeenCalledWith(
        expect.objectContaining({ title: "Could not toggle the rule", type: "error" }),
      ),
    );
  });

  it("deletes a rule after confirmation", async () => {
    setup();
    mockDelete.mockResolvedValueOnce(undefined);
    const del = await screen.findByRole("button", { name: /Delete rule/ });
    vi.spyOn(window, "confirm").mockReturnValue(true);
    fireEvent.click(del);
    await waitFor(() => expect(mockDelete).toHaveBeenCalledWith("acme", "ENG", "rule-1"));
    (window.confirm as unknown as { mockRestore: () => void }).mockRestore();
  });

  it("creates a rule from the builder", async () => {
    setup();
    mockCreate.mockResolvedValueOnce({ ...rule, id: "rule-2", name: "Ping on done" });
    fireEvent.click(await screen.findByRole("button", { name: /New rule/ }));
    fireEvent.change(await screen.findByLabelText("Name"), {
      target: { value: "Ping on done" },
    });
    fireEvent.change(await screen.findByLabelText("To state"), {
      target: { value: "state-started" },
    });
    fireEvent.change(await screen.findByLabelText("Action 1 comment"), {
      target: { value: "done!" },
    });
    fireEvent.click(await screen.findByRole("button", { name: /Create rule/ }));
    await waitFor(() => expect(mockCreate).toHaveBeenCalled());
    const input = mockCreate.mock.calls[0][2];
    expect(input.name).toBe("Ping on done");
    expect(input.trigger).toEqual({
      type: "issue.state_changed",
      from_states: null,
      to_states: ["state-started"],
    });
    expect(input.actions).toEqual([{ type: "add_comment", body: "done!" }]);
  });

  it("hides mutation controls for guests (read-only view)", async () => {
    setup(false);
    expect(await screen.findByText("Assign QA on start")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /New rule/ })).toBeNull();
    expect(screen.queryByRole("switch")).toBeNull();
  });

  it("renders recent runs with rule name, issue link, relative time, and action chips", async () => {
    setup();
    expect(await screen.findByText("Recent runs")).toBeTruthy();
    // Rule name + issue display-id link.
    const issueLink = await screen.findByRole("link", { name: "ENG-42" });
    expect(issueLink.getAttribute("href")).toBe("/w/acme/p/ENG/i/issue-9");
    // Relative time.
    expect(await screen.findByText("5m ago")).toBeTruthy();
    // Per-action chips: ok and failed.
    expect(await screen.findByText("assign · ok")).toBeTruthy();
    expect(await screen.findByText("add_comment · failed")).toBeTruthy();
    // Error text on the failed action.
    expect(await screen.findByText(/boom/)).toBeTruthy();
  });

  it("shows an honest empty state when there are no runs", async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    mockFetchRuns.mockResolvedValue([]);
    render(
      <MemoryRouter>
        <QueryClientProvider client={client}>
          <AutomationsSection slug="acme" identifier="ENG" canEdit={true} />
        </QueryClientProvider>
      </MemoryRouter>,
    );
    expect(await screen.findByText(/No automation runs yet/)).toBeTruthy();
  });

  it("shows an honest error when the runs feed fails", async () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    mockFetchRuns.mockRejectedValueOnce(new Error("nope"));
    render(
      <MemoryRouter>
        <QueryClientProvider client={client}>
          <AutomationsSection slug="acme" identifier="ENG" canEdit={true} />
        </QueryClientProvider>
      </MemoryRouter>,
    );
    expect(await screen.findByText(/Could not load automation runs/)).toBeTruthy();
  });
});
