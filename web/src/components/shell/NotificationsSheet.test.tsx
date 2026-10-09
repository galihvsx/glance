// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, useLocation } from "react-router-dom";
import { useEffect, type ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import NotificationsSheet from "./NotificationsSheet";
import { ShellProvider, useShell } from "./shell-context";
import type { NotificationItem } from "../../lib/notifications";

const postCalls: string[] = [];

const notification: NotificationItem = {
  id: "n1",
  user_id: "u1",
  type: "issue.assigned",
  title: "You were assigned to fix login",
  payload: { actor_id: "a1" },
  read_at: null,
  created_at: new Date(Date.now() - 60_000).toISOString(),
};

vi.mock("../../lib/api", () => ({
  api: {
    get: (path: string) => {
      if (path.startsWith("/api/v1/notifications")) {
        return Promise.resolve({
          notifications: [notification],
          unread_count: 1,
        });
      }
      if (path === "/api/v1/workspaces") {
        return Promise.resolve({ workspaces: [] });
      }
      return Promise.reject(new Error(`unexpected GET ${path}`));
    },
    post: (path: string) => {
      postCalls.push(path);
      return Promise.resolve({ ok: true });
    },
  },
  ApiError: class ApiError extends Error {},
}));

function LocationProbe() {
  const { pathname } = useLocation();
  return <div data-testid="location">{pathname}</div>;
}

/** Opens the sheet on mount via the (stub) shell context. */
function OpenSheet({ children }: { children: ReactNode }) {
  const { setNotificationsOpen } = useShell();
  useEffect(() => {
    setNotificationsOpen(true);
  }, [setNotificationsOpen]);
  return <>{children}</>;
}

function renderSheet() {
  return render(
    <QueryClientProvider client={new QueryClient()}>
      <ShellProvider>
        <MemoryRouter initialEntries={["/"]}>
          <OpenSheet>
            <NotificationsSheet />
            <LocationProbe />
          </OpenSheet>
        </MemoryRouter>
      </ShellProvider>
    </QueryClientProvider>,
  );
}

describe("NotificationsSheet", () => {
  it("opens via context and shows the unread list", async () => {
    renderSheet();

    await waitFor(() => {
      expect(screen.getByText("You were assigned to fix login")).toBeInTheDocument();
    });
    expect(screen.getByLabelText("1 unread")).toBeInTheDocument();
  });

  it("marks all read via the shared mutation", async () => {
    postCalls.length = 0;
    renderSheet();

    // Wait for the unread count so the button is enabled before clicking.
    await waitFor(() => {
      expect(screen.getByLabelText("1 unread")).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText("Mark all read"));

    await waitFor(() => {
      expect(postCalls).toContain("/api/v1/notifications/read");
    });
  });

  it("marks one read and closes on row click", async () => {
    postCalls.length = 0;
    renderSheet();

    await waitFor(() => {
      expect(screen.getByText("You were assigned to fix login")).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText("You were assigned to fix login"));

    await waitFor(() => {
      expect(postCalls).toContain("/api/v1/notifications/n1/read");
    });
    // Sheet closed after the click.
    await waitFor(() => {
      expect(screen.queryByText("Notifications")).not.toBeInTheDocument();
    });
  });

  it("navigates to /notifications from the footer link and closes", async () => {
    renderSheet();

    await waitFor(() => {
      expect(screen.getByText("View all notifications")).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText("View all notifications"));

    await waitFor(() => {
      expect(screen.getByTestId("location")).toHaveTextContent("/notifications");
    });
  });

  it("renders closed (and does not throw) outside a shell provider", () => {
    render(
      <QueryClientProvider client={new QueryClient()}>
        <MemoryRouter initialEntries={["/"]}>
          <NotificationsSheet />
        </MemoryRouter>
      </QueryClientProvider>,
    );
    expect(screen.queryByText("Notifications")).not.toBeInTheDocument();
  });
});
