// @vitest-environment jsdom
// C89T1: sidebar teamspace grouping + teamspace manage dialog —
// grouped rendering (membership + order), collapse persistence,
// deep link into a collapsed group, create/rename/delete flows,
// Ungrouped having no menu, archived projects excluded.

import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import AppSidebar from "./AppSidebar";
import { ShellProvider, type ShellContextValue } from "./shell-context";
import { NOTIFICATION_KEYS } from "../../lib/notifications";
import { api } from "../../lib/api";
import type { Project, Teamspace, Workspace } from "../../lib/types";
import { workspacesKey } from "../../lib/useWorkspaces";
import { makeQueryClient, stubMatchMedia, ShellCapture } from "./test-utils";

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

const get = () => api.get as unknown as Mock;
const post = () => api.post as unknown as Mock;
const patch = () => api.patch as unknown as Mock;
const del = () => api.del as unknown as Mock;

const workspaces: Workspace[] = [
  {
    id: "w1",
    slug: "acme",
    name: "Acme Corp",
    role: 20,
    slack_configured: false,
    github_webhook_configured: false,
    created_at: "",
    updated_at: "",
  },
];

// Server orders teamspaces by name: Mobile before Platform.
const seedTeamspaces = (): Teamspace[] => [
  { id: "ts-mobile", workspace_id: "w1", name: "Mobile", project_count: 1, created_at: "", updated_at: "" },
  { id: "ts-platform", workspace_id: "w1", name: "Platform", project_count: 2, created_at: "", updated_at: "" },
];

const seedProjects = (): Project[] => [
  { id: "p-eng", workspace_id: "w1", identifier: "ENG", labels: [], name: "Engine", description: "", parallel_cycles: false, teamspace_id: "ts-platform", created_at: "", updated_at: "" },
  { id: "p-api", workspace_id: "w1", identifier: "API", labels: [], name: "API Gateway", description: "", parallel_cycles: false, teamspace_id: "ts-platform", created_at: "", updated_at: "" },
  { id: "p-and", workspace_id: "w1", identifier: "AND", labels: [], name: "Android App", description: "", parallel_cycles: false, teamspace_id: "ts-mobile", created_at: "", updated_at: "" },
  { id: "p-web", workspace_id: "w1", identifier: "WEB", labels: [], name: "Website", description: "", parallel_cycles: false, teamspace_id: null, created_at: "", updated_at: "" },
  // Archived: keeps its teamspace_id but must never render in the sidebar.
  { id: "p-old", workspace_id: "w1", identifier: "OLD", labels: [], name: "Old Service", description: "", parallel_cycles: false, teamspace_id: "ts-platform", archived_at: "2026-01-01T00:00:00Z", created_at: "", updated_at: "" },
];

// Mutable mock DB: mutations update it, refetches read it back.
let db: { teamspaces: Teamspace[]; projects: Project[] };

function installApiMocks() {
  get().mockImplementation((url: string) => {
    if (url.includes("/teamspaces")) return Promise.resolve({ teamspaces: db.teamspaces });
    if (url.includes("/initiatives")) return Promise.resolve({ initiatives: [] });
    if (url.includes("/objectives")) return Promise.resolve({ objectives: [] });
    if (url.includes("/projects")) return Promise.resolve({ projects: db.projects });
    return Promise.resolve({});
  });
  post().mockImplementation((url: string, body: { name: string }) => {
    if (url.includes("/teamspaces")) {
      const ts: Teamspace = { id: "ts-new", workspace_id: "w1", name: body.name, project_count: 0, created_at: "", updated_at: "" };
      db.teamspaces = [...db.teamspaces, ts];
      return Promise.resolve(ts);
    }
    return Promise.resolve({});
  });
  patch().mockImplementation((url: string, body: { name: string }) => {
    const id = url.split("/").pop()!;
    const ts = db.teamspaces.find((t) => t.id === id);
    if (ts) ts.name = body.name;
    return Promise.resolve(ts);
  });
  del().mockImplementation((url: string) => {
    const id = url.split("/").pop()!;
    const n = db.projects.filter((p) => p.teamspace_id === id).length;
    db.projects = db.projects.map((p) =>
      p.teamspace_id === id ? { ...p, teamspace_id: null } : p,
    );
    db.teamspaces = db.teamspaces.filter((t) => t.id !== id);
    return Promise.resolve({ id, ungrouped_projects: n });
  });
}

function renderSidebar(path: string, routePath: string) {
  const capture: { current: ShellContextValue | null } = { current: null };
  const qc = makeQueryClient([
    [workspacesKey, workspaces],
    [NOTIFICATION_KEYS.unread, 0],
    [["projects", "acme"], db.projects],
    [["teamspaces", "acme"], db.teamspaces],
  ]);
  render(
    <MemoryRouter initialEntries={[path]}>
      <QueryClientProvider client={qc}>
        <Routes>
          <Route
            path={routePath}
            element={
              <ShellProvider>
                <AppSidebar />
                <ShellCapture capture={capture} />
              </ShellProvider>
            }
          />
        </Routes>
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return capture;
}

function projectsSection(): HTMLElement {
  return document.querySelector('section[aria-label="Projects"]') as HTMLElement;
}

/** The group <div> wrapping a header button (header div → group div). */
function groupDivFor(headerButton: HTMLElement): HTMLElement {
  return headerButton.closest("div")!.parentElement as HTMLElement;
}

function groupHeaderButton(name: string): HTMLElement {
  return screen.getByRole("button", {
    name: new RegExp(`(Collapse|Expand) ${name} group`),
  });
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

beforeEach(() => {
  localStorage.clear();
  stubMatchMedia(false);
  db = { teamspaces: seedTeamspaces(), projects: seedProjects() };
  installApiMocks();
});

describe("teamspace grouping", () => {
  it("renders teamspaces in order with correct membership, Ungrouped last", () => {
    renderSidebar("/w/acme", "/w/:slug");
    const section = projectsSection();
    const text = section.textContent ?? "";
    expect(text.indexOf("Mobile")).toBeLessThan(text.indexOf("Platform"));
    expect(text.indexOf("Platform")).toBeLessThan(text.indexOf("Ungrouped"));

    expect(
      within(groupDivFor(groupHeaderButton("Platform"))).getByText("Engine"),
    ).toBeTruthy();
    expect(
      within(groupDivFor(groupHeaderButton("Platform"))).getByText("API Gateway"),
    ).toBeTruthy();
    expect(
      within(groupDivFor(groupHeaderButton("Mobile"))).getByText("Android App"),
    ).toBeTruthy();
    expect(
      within(groupDivFor(groupHeaderButton("Ungrouped"))).getByText("Website"),
    ).toBeTruthy();
    // Live counts from the project list (archived Old Service excluded).
    expect(groupHeaderButton("Platform").textContent).toContain("2");
    expect(groupHeaderButton("Mobile").textContent).toContain("1");
    expect(groupHeaderButton("Ungrouped").textContent).toContain("1");
  });

  it("excludes archived projects from sidebar groups", () => {
    renderSidebar("/w/acme", "/w/:slug");
    expect(screen.queryByText("Old Service")).toBeNull();
  });

  it("Ungrouped has no teamspace menu", () => {
    renderSidebar("/w/acme", "/w/:slug");
    expect(
      screen.queryByRole("button", { name: "Ungrouped teamspace menu" }),
    ).toBeNull();
    expect(
      screen.getByRole("button", { name: "Platform teamspace menu" }),
    ).toBeTruthy();
  });

  it("group collapse persists per teamspace id and re-expands", () => {
    renderSidebar("/w/acme", "/w/:slug");
    fireEvent.click(groupHeaderButton("Platform"));
    expect(screen.queryByText("Engine")).toBeNull();
    expect(
      localStorage.getItem("glance:teamspace-group-collapsed:ts-platform"),
    ).toBe("true");

    fireEvent.click(groupHeaderButton("Platform"));
    expect(screen.getByText("Engine")).toBeTruthy();
    expect(
      localStorage.getItem("glance:teamspace-group-collapsed:ts-platform"),
    ).toBe("false");
  });

  it("a deep link into a collapsed group expands that group", () => {
    localStorage.setItem(
      "glance:teamspace-group-collapsed:ts-platform",
      "true",
    );
    renderSidebar("/w/acme/p/ENG", "/w/:slug/p/:identifier");
    // The routed project is visible with its sub-links, not hidden.
    expect(screen.getByText("Engine")).toBeTruthy();
    expect(screen.getByText("Work items")).toBeTruthy();
    expect(
      localStorage.getItem("glance:teamspace-group-collapsed:ts-platform"),
    ).toBe("false");
  });
});

describe("teamspace manage dialog", () => {
  it("create rejects an empty name inline without calling the API", async () => {
    renderSidebar("/w/acme", "/w/:slug");
    fireEvent.click(screen.getByRole("button", { name: "New teamspace" }));
    const create = await screen.findByRole("button", { name: "Create teamspace" });
    fireEvent.click(create);
    expect(
      await screen.findByText("Teamspace name is required."),
    ).toBeTruthy();
    expect(post()).not.toHaveBeenCalled();
  });

  it("create adds a group header after refetch", async () => {
    renderSidebar("/w/acme", "/w/:slug");
    fireEvent.click(screen.getByRole("button", { name: "New teamspace" }));
    const input = await screen.findByLabelText("Name");
    fireEvent.change(input, { target: { value: "  Data  " } });
    fireEvent.click(screen.getByRole("button", { name: "Create teamspace" }));
    // React Query starts the mutation asynchronously; await the call.
    await waitFor(() => {
      expect(post()).toHaveBeenCalledWith(
        "/api/v1/workspaces/acme/teamspaces",
        { name: "Data" },
      );
    });
    await waitFor(() => {
      expect(screen.getByText("Data")).toBeTruthy();
    });
  });

  it("rename updates the group header", async () => {
    renderSidebar("/w/acme", "/w/:slug");
    fireEvent.click(
      screen.getByRole("button", { name: "Platform teamspace menu" }),
    );
    fireEvent.click(await screen.findByText("Rename teamspace"));
    const input = await screen.findByLabelText("Name");
    expect((input as HTMLInputElement).value).toBe("Platform");
    fireEvent.change(input, { target: { value: "Platform X" } });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => {
      expect(patch()).toHaveBeenCalledWith(
        "/api/v1/workspaces/acme/teamspaces/ts-platform",
        { name: "Platform X" },
      );
    });
    await waitFor(() => {
      expect(screen.getByText("Platform X")).toBeTruthy();
    });
    expect(screen.queryByText("Platform")).toBeNull();
  });

  it("delete confirm states the ungrouped count and ungroups on confirm", async () => {
    renderSidebar("/w/acme", "/w/:slug");
    fireEvent.click(
      screen.getByRole("button", { name: "Mobile teamspace menu" }),
    );
    fireEvent.click(await screen.findByText("Delete teamspace"));
    // The confirm copy is split across text nodes, so assert on the
    // dialog's combined text rather than a single text node.
    const dialog = await screen.findByRole("dialog");
    expect(dialog.textContent).toContain(
      "1 project will move to Ungrouped — no projects will be deleted.",
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Delete teamspace" }),
    );
    await waitFor(() => {
      expect(del()).toHaveBeenCalledWith(
        "/api/v1/workspaces/acme/teamspaces/ts-mobile",
      );
    });
    await waitFor(() => {
      // Android App now lives under Ungrouped (Website + Android App).
      expect(
        within(groupDivFor(groupHeaderButton("Ungrouped"))).getByText(
          "Android App",
        ),
      ).toBeTruthy();
    });
    expect(screen.queryByText("Mobile")).toBeNull();
  });
});
