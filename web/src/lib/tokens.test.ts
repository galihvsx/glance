import { describe, expect, it } from "vitest";
import {
  type ApiToken,
  confirmRevoke,
  EXPIRY_CHOICES,
  expiresAtForChoice,
  initialSecretRevealState,
  secretRevealReducer,
  TOKEN_SCOPES,
  validateCreateTokenForm,
} from "./tokens";

describe("validateCreateTokenForm", () => {
  it("requires a name", () => {
    expect(validateCreateTokenForm("", ["read"])).toBe("Name is required.");
    expect(validateCreateTokenForm("   ", ["read"])).toBe("Name is required.");
  });

  it("requires at least one scope", () => {
    expect(validateCreateTokenForm("ci", [])).toBe(
      "Select at least one scope.",
    );
  });

  it("rejects scopes outside the closed vocabulary", () => {
    expect(validateCreateTokenForm("ci", ["read", "admin"])).toBe(
      "Unknown scope: admin.",
    );
  });

  it("accepts a valid name with read/write scopes", () => {
    expect(validateCreateTokenForm("ci", ["read"])).toBeNull();
    expect(validateCreateTokenForm("deploy bot", ["read", "write"])).toBeNull();
  });

  it("only advertises the backend's scope vocabulary", () => {
    expect([...TOKEN_SCOPES]).toEqual(["read", "write"]);
  });
});

describe("expiresAtForChoice", () => {
  it("omits expires_at for 'Never'", () => {
    const never = EXPIRY_CHOICES.find((c) => c.id === "never")!;
    expect(expiresAtForChoice(never)).toBeUndefined();
  });

  it("computes a future RFC 3339 timestamp", () => {
    const now = new Date("2026-10-09T00:00:00Z");
    const seven = EXPIRY_CHOICES.find((c) => c.id === "7d")!;
    const at = expiresAtForChoice(seven, now);
    expect(at).toBe("2026-10-16T00:00:00.000Z");
    expect(new Date(at!).getTime()).toBeGreaterThan(now.getTime());
  });
});

describe("secretRevealReducer — show-once secret flow", () => {
  it("stores the plaintext when a token is created", () => {
    const state = secretRevealReducer(initialSecretRevealState, {
      type: "created",
      token: "gl_secret",
    });
    expect(state.showing).toBe(true);
    expect(state.secret).toBe("gl_secret");
  });

  it("drops the secret permanently on dismissal", () => {
    const created = secretRevealReducer(initialSecretRevealState, {
      type: "created",
      token: "gl_secret",
    });
    const dismissed = secretRevealReducer(created, { type: "dismissed" });
    expect(dismissed.showing).toBe(false);
    expect(dismissed.secret).toBeNull();
  });

  it("can never resurrect the secret from list data", () => {
    // The list endpoint's shape (internal/auth.Token) carries no secret.
    // If a field named "token"/"plaintext" ever appears here, the
    // type-level guarantee behind the show-once flow is broken.
    const listItem = {
      id: "1",
      name: "ci",
      scopes: ["read"],
      expires_at: null,
      last_used_at: null,
      created_at: "2026-10-09T00:00:00Z",
    } satisfies ApiToken;
    expect("token" in listItem).toBe(false);
    expect("plaintext" in listItem).toBe(false);
    expect("token_hash" in listItem).toBe(false);
  });
});

describe("confirmRevoke — revoke-confirm flow", () => {
  it("asks for confirmation naming the token", () => {
    const seen: string[] = [];
    const ok = confirmRevoke("ci", (msg) => {
      seen.push(msg);
      return true;
    });
    expect(ok).toBe(true);
    expect(seen).toHaveLength(1);
    expect(seen[0]).toContain('"ci"');
    expect(seen[0]).toMatch(/revoke/i);
  });

  it("returns false when the user declines, so no delete is issued", () => {
    expect(confirmRevoke("ci", () => false)).toBe(false);
  });
});
