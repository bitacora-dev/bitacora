import { describe, expect, it } from "vitest";
import { authSessionFallback, loginScreen, type AuthSession } from "./login";

function session(overrides: Partial<AuthSession> = {}): AuthSession {
  return {
    auth_enabled: true,
    authenticated: false,
    local_enabled: false,
    oidc_enabled: false,
    local_pending_initialization: false,
    ...overrides,
  };
}

describe("loginScreen", () => {
  it("offers the local password and TOTP form when the credential exists", () => {
    expect(loginScreen(session({ local_enabled: true }))).toEqual({
      localForm: true,
      oidcLink: false,
      notice: "none",
    });
  });

  it("offers both routes when OIDC is configured alongside the local credential", () => {
    expect(loginScreen(session({ local_enabled: true, oidc_enabled: true }))).toEqual({
      localForm: true,
      oidcLink: true,
      notice: "none",
    });
  });

  // The state this change introduces: switched on, credential not created yet.
  // There is no form to show, because ADR-0023 keeps credential creation on
  // the server's TTY — the screen has to say which step is missing instead.
  it("explains the pending CLI step instead of showing a form it cannot submit", () => {
    expect(loginScreen(session({ local_pending_initialization: true }))).toEqual({
      localForm: false,
      oidcLink: false,
      notice: "pending_initialization",
    });
  });

  it("never offers a form while initialization is pending, even if a source is reported", () => {
    expect(loginScreen(session({ local_enabled: true, local_pending_initialization: true })).localForm).toBe(false);
  });

  it("reports an unavailable login when the hub has no human boundary", () => {
    expect(loginScreen(session({ auth_enabled: false }))).toEqual({
      localForm: false,
      oidcLink: false,
      notice: "unavailable",
    });
  });

  it("reports an unavailable login when the boundary is wired but offers no route", () => {
    expect(loginScreen(session()).notice).toBe("unavailable");
  });

  it("treats an unreachable session endpoint as an unavailable login, never as an open hub", () => {
    expect(loginScreen(authSessionFallback())).toEqual({
      localForm: false,
      oidcLink: false,
      notice: "unavailable",
    });
  });
});
