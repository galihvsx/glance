import { describe, expect, it } from "vitest";
import {
  auditCategory,
  buildWorkspaceAuditExportUrl,
  buildWorkspaceAuditLogUrl,
  datetimeLocalToISO,
  truncateAuditId,
  WORKSPACE_AUDIT_ALL_ACTIONS,
  workspaceAuditParams,
} from "./workspaceAudit";

describe("workspaceAudit", () => {
  it("lists exactly the 14 actions the backend emits (no token actions)", () => {
    expect(WORKSPACE_AUDIT_ALL_ACTIONS).toHaveLength(14);
    expect(WORKSPACE_AUDIT_ALL_ACTIONS).not.toContain(
      "integration.token_created",
    );
    expect(WORKSPACE_AUDIT_ALL_ACTIONS).not.toContain(
      "integration.token_revoked",
    );
    expect(WORKSPACE_AUDIT_ALL_ACTIONS).toContain("member.role_changed");
    expect(WORKSPACE_AUDIT_ALL_ACTIONS).toContain(
      "integration.slack_disconnected",
    );
  });

  it("maps filters onto the backend query params", () => {
    const url = buildWorkspaceAuditLogUrl("acme", 2, {
      action: "member.role_changed",
      actor: "admin@",
      entityType: "member",
      since: "2026-10-16T19:00",
      until: "2026-10-17T19:00",
    });
    expect(url).toContain("/api/v1/workspaces/acme/audit-log?");
    const query = new URLSearchParams(url.split("?")[1]);
    expect(query.get("page")).toBe("2");
    expect(query.get("per_page")).toBe("25");
    expect(query.get("action")).toBe("member.role_changed");
    expect(query.get("actor")).toBe("admin@");
    expect(query.get("entity_type")).toBe("member");
    // datetime-local values become RFC 3339 UTC.
    expect(query.get("since")).toBe(
      new Date("2026-10-16T19:00").toISOString(),
    );
    expect(query.get("until")).toBe(
      new Date("2026-10-17T19:00").toISOString(),
    );
  });

  it("drops empty filters and never sends a category param", () => {
    const params = workspaceAuditParams({ category: "member" });
    expect(params.toString()).toBe("");
    expect(params.has("category")).toBe(false);
  });

  it("builds the export URL with format=csv and the same filters, no pagination", () => {
    const url = buildWorkspaceAuditExportUrl("acme", {
      action: "project.created",
      actor: "owner@",
    });
    expect(url).toContain(
      "/api/v1/workspaces/acme/audit-log/export?",
    );
    const query = new URLSearchParams(url.split("?")[1]);
    expect(query.get("format")).toBe("csv");
    expect(query.get("action")).toBe("project.created");
    expect(query.get("actor")).toBe("owner@");
    expect(query.has("page")).toBe(false);
    expect(query.has("per_page")).toBe(false);
  });

  it("converts datetime-local to RFC 3339, undefined for empty/invalid", () => {
    expect(datetimeLocalToISO("2026-10-16T19:00")).toBe(
      new Date("2026-10-16T19:00").toISOString(),
    );
    expect(datetimeLocalToISO("")).toBeUndefined();
    expect(datetimeLocalToISO(undefined)).toBeUndefined();
    expect(datetimeLocalToISO("not-a-date")).toBeUndefined();
  });

  it("derives the category from the dotted action", () => {
    expect(auditCategory("member.role_changed")).toBe("member");
    expect(auditCategory("integration.webhook_created")).toBe("integration");
    expect(auditCategory("workspace.settings_updated")).toBe("workspace");
  });

  it("truncates long opaque ids but shows invite emails in full", () => {
    expect(truncateAuditId("9f2c3d4e-1111-2222-3333-444455556666")).toBe(
      "9f2c3d4e…",
    );
    expect(truncateAuditId("short")).toBe("short");
    expect(truncateAuditId("invited-person@example.com")).toBe(
      "invited-person@example.com",
    );
  });
});
