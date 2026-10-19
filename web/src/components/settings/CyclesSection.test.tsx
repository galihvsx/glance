// @vitest-environment jsdom
// Component tests for the project cycles settings (C78T1): the
// "Parallel cycles" toggle — renders from the project payload, edits are
// member (15)+ only with guests read-only, failed toggles surface the
// error and revert, and the Plane wording + grandfathering note are shown.

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import CyclesSection from "./CyclesSection";
import { api, ApiError } from "../../lib/api";
import { makeQueryClient } from "../shell/test-utils";
import type { Project } from "../../lib/types";

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
const patch = () => api.patch as unknown as Mock;

const PROJECT_PATH = "/api/v1/workspaces/acme/projects/ENG";

const project: Project = {
  id: "p-eng",
  workspace_id: "w1",
  identifier: "ENG",
  name: "Engineering",
  description: "",
  labels: [],
  parallel_cycles: false,
  created_at: "",
  updated_at: "",
};

function renderSection(canEdit: boolean, p: Project = project) {
  get().mockResolvedValue(p);
  const qc = makeQueryClient();
  render(
    <QueryClientProvider client={qc}>
      <CyclesSection slug="acme" identifier="ENG" canEdit={canEdit} />
    </QueryClientProvider>,
  );
}

async function toggleSwitch() {
  const sw = await screen.findByRole("switch", { name: "Parallel cycles" });
  return sw;
}

beforeEach(() => {
  get().mockReset();
  patch().mockReset();
});

afterEach(cleanup);

describe("CyclesSection", () => {
  it("renders the parallel cycles toggle off when the project has it off", async () => {
    renderSection(true);
    const sw = await toggleSwitch();
    expect(sw.getAttribute("aria-checked")).toBe("false");
  });

  it("renders the toggle on when the project has parallel cycles enabled", async () => {
    renderSection(true, { ...project, parallel_cycles: true });
    const sw = await toggleSwitch();
    expect(sw.getAttribute("aria-checked")).toBe("true");
  });

  it("shows the Plane description wording and the grandfathering note", async () => {
    renderSection(true);
    await toggleSwitch();
    expect(
      screen.getByText(/only one cycle can be active at a time and cycles can't overlap/),
    ).toBeTruthy();
    expect(
      screen.getByText(/doesn't change existing overlapping cycles/),
    ).toBeTruthy();
  });

  it("member can toggle parallel cycles on through the PATCH contract", async () => {
    renderSection(true);
    const sw = await toggleSwitch();
    patch().mockResolvedValue({ ...project, parallel_cycles: true });
    fireEvent.click(sw);
    await waitFor(() => {
      expect(patch()).toHaveBeenCalledWith(PROJECT_PATH, {
        parallel_cycles: true,
      });
    });
    await waitFor(() => {
      expect(sw.getAttribute("aria-checked")).toBe("true");
    });
  });

  it("member can toggle parallel cycles off through the PATCH contract", async () => {
    renderSection(true, { ...project, parallel_cycles: true });
    const sw = await toggleSwitch();
    patch().mockResolvedValue({ ...project, parallel_cycles: false });
    fireEvent.click(sw);
    await waitFor(() => {
      expect(patch()).toHaveBeenCalledWith(PROJECT_PATH, {
        parallel_cycles: false,
      });
    });
    await waitFor(() => {
      expect(sw.getAttribute("aria-checked")).toBe("false");
    });
  });

  it("guest sees a disabled read-only switch and cannot toggle", async () => {
    renderSection(false);
    const sw = await toggleSwitch();
    // base-ui renders the disabled state as data-disabled, not a native
    // disabled property.
    expect(sw).toHaveAttribute("data-disabled");
    fireEvent.click(sw);
    expect(patch()).not.toHaveBeenCalled();
  });

  it("a failed toggle shows the error inline and reverts to the server value", async () => {
    renderSection(true);
    const sw = await toggleSwitch();
    patch().mockRejectedValue(
      new ApiError(403, "Guests cannot edit project settings", "forbidden"),
    );
    fireEvent.click(sw);
    await waitFor(() => {
      expect(
        screen.getByText("Guests cannot edit project settings"),
      ).toBeTruthy();
    });
    // The optimistic flip reverts to the server value (off).
    await waitFor(() => {
      expect(sw.getAttribute("aria-checked")).toBe("false");
    });
  });

  it("shows an error when the project fails to load", async () => {
    get().mockRejectedValue(new ApiError(500, "boom", "internal"));
    const qc = makeQueryClient();
    render(
      <QueryClientProvider client={qc}>
        <CyclesSection slug="acme" identifier="ENG" canEdit />
      </QueryClientProvider>,
    );
    await waitFor(() => {
      expect(screen.getByText("boom")).toBeTruthy();
    });
  });
});
