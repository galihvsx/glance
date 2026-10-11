// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { userEvent } from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import ProjectTemplatesSection from "./ProjectTemplatesSection";
import type { ProjectTemplate } from "../../lib/projectTemplates";

const mockTemplates: ProjectTemplate[] = [
  {
    id: "t1",
    workspace_id: "w1",
    name: "Web team",
    description: "Standard web setup",
    payload: {
      states: [{ name: "Icebox", group: "backlog", color: "#6b7280" }],
      labels: [{ name: "bug", color: "#ef4444" }],
      modules: [],
    },
    created_at: "2026-10-11T10:00:00Z",
    updated_at: "2026-10-11T10:00:00Z",
  },
];

const postMock = vi.fn();
const patchMock = vi.fn();
const delMock = vi.fn();

vi.mock("../../lib/api", () => ({
  api: {
    get: vi.fn((path: string) => {
      if (path.includes("/templates")) {
        return Promise.resolve({ templates: mockTemplates });
      }
      return Promise.reject(new Error("unexpected path: " + path));
    }),
    post: (...args: unknown[]) => postMock(...args),
    patch: (...args: unknown[]) => patchMock(...args),
    del: (...args: unknown[]) => delMock(...args),
  },
  ApiError: class ApiError extends Error {},
}));

function renderSection() {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <ProjectTemplatesSection slug="acme" />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("ProjectTemplatesSection", () => {
  it("lists templates with a seed summary", async () => {
    renderSection();
    expect(await screen.findByText("Web team")).toBeTruthy();
    expect(
      await screen.findByText(/1 states · 1 labels/),
    ).toBeTruthy();
  });

  it("opens the create dialog and posts the assembled payload", async () => {
    const user = userEvent.setup();
    postMock.mockResolvedValue({ id: "t2", name: "API team" });
    renderSection();
    await screen.findByText("Web team");

    await user.click(screen.getByText("New template"));
    expect(await screen.findByText("New project template")).toBeTruthy();

    await user.type(screen.getByLabelText("Name"), "API team");
    await user.click(screen.getByText("Add state"));
    await user.type(screen.getByLabelText("State 1 name"), "Icebox");
    await user.click(screen.getByText("Add label"));
    await user.type(screen.getByLabelText("Label 1 name"), "bug");
    await user.click(screen.getByText("Create template"));

    await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
    const [path, body] = postMock.mock.calls[0] as [string, Record<string, unknown>];
    expect(path).toBe("/api/v1/workspaces/acme/templates");
    expect(body.name).toBe("API team");
    const payload = body.payload as {
      states: { name: string; group: string }[];
      labels: { name: string }[];
    };
    expect(payload.states[0]).toMatchObject({ name: "Icebox", group: "backlog" });
    expect(payload.labels[0].name).toBe("bug");
  });

  it("deletes after confirmation", async () => {
    const user = userEvent.setup();
    const confirmSpy = vi
      .spyOn(window, "confirm")
      .mockReturnValue(true);
    delMock.mockResolvedValue(undefined);
    renderSection();
    await screen.findByText("Web team");

    await user.click(screen.getByLabelText("Delete template Web team"));
    await waitFor(() => expect(delMock).toHaveBeenCalledTimes(1));
    expect(delMock).toHaveBeenCalledWith("/api/v1/workspaces/acme/templates/t1");
    confirmSpy.mockRestore();
  });
});
