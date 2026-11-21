import { describe, expect, it } from "vitest";
import { groupSecret, passwordStrength, setupErrorCode } from "./setup";

describe("passwordStrength", () => {
  it("rejects anything under the server minimum", () => {
    expect(passwordStrength("short1!")).toBe("too_short");
    expect(passwordStrength("elevenchars")).toBe("too_short");
  });

  it("rates a password equal to the username as weak", () => {
    expect(passwordStrength("operator-name", "Operator-Name")).toBe("weak");
  });

  it("rates repetitive passwords as weak", () => {
    expect(passwordStrength("aaaaaaaaaaaaaaaa")).toBe("weak");
  });

  it("rewards length first", () => {
    expect(passwordStrength("lowercaseonlyx")).toBe("weak");
    expect(passwordStrength("Mixed case 12")).toBe("fair");
    expect(passwordStrength("correct horse battery staple")).toBe("strong");
  });
});

describe("setupErrorCode", () => {
  it("keeps the codes the server sends", () => {
    expect(setupErrorCode(401, { error: "invalid_setup_token" })).toBe("invalid_setup_token");
    expect(setupErrorCode(400, { error: "password_too_short", min_password_length: 12 })).toBe("password_too_short");
    expect(setupErrorCode(503, { error: "setup_state_read_only" })).toBe("setup_state_read_only");
  });

  it("reports throttling and hides anything unexpected", () => {
    expect(setupErrorCode(429, null)).toBe("rate_limited");
    expect(setupErrorCode(500, { error: "open /etc/bitacora: permission denied" })).toBe("setup_failed");
    expect(setupErrorCode(400, "not json")).toBe("setup_failed");
  });
});

describe("groupSecret", () => {
  it("splits the base32 secret into blocks of four", () => {
    expect(groupSecret("ABCDEFGHIJ")).toBe("ABCD EFGH IJ");
  });
});
