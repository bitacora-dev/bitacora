import { describe, expect, it } from "vitest";
import { agentSetupCommand } from "./enrollment";
import type { CreateHostResponse } from "./api";

const host: CreateHostResponse = {
  host_id: "01J8XQZK9V2M0000000000000A",
  token: "s3cr3t-token-value",
  created_at: "2026-09-02T21:00:00Z",
  host_id_path: "/var/lib/bitacora/host_id",
  token_path: "/etc/bitacora/token",
};

describe("agentSetupCommand", () => {
  const command = agentSetupCommand({ hubURL: "https://bitacora.example", host });

  it("seeds the host_id the hub issued, so the agent doesn't invent its own", () => {
    expect(command).toContain("'01J8XQZK9V2M0000000000000A' > /var/lib/bitacora/host_id");
  });

  it("writes the token to the file the hub named, never as a command-line argument", () => {
    expect(command).toContain("'s3cr3t-token-value' > /etc/bitacora/token");
    expect(command).not.toContain("-token=s3cr3t-token-value");
    expect(command).toContain("-token-file=/etc/bitacora/token");
  });

  it("restricts the token file to its owner", () => {
    expect(command).toContain("chmod 0600 /etc/bitacora/token");
  });

  it("creates both parent directories before writing into them", () => {
    const lines = command.split("\n");
    expect(lines.indexOf("install -d -m 0755 /var/lib/bitacora")).toBeLessThan(
      lines.findIndex((l) => l.includes("> /var/lib/bitacora/host_id")),
    );
    expect(lines.indexOf("install -d -m 0750 /etc/bitacora")).toBeLessThan(
      lines.findIndex((l) => l.includes("> /etc/bitacora/token")),
    );
  });

  it("points the agent at the hub the UI is served from", () => {
    expect(command).toContain("-hub-url='https://bitacora.example'");
  });

  it("quotes values so a shell metacharacter can't escape into a command", () => {
    const hostile = agentSetupCommand({
      hubURL: "https://hub.example",
      host: { ...host, token: "a'; rm -rf /; echo '" },
    });
    expect(hostile).toContain(`'a'\\''; rm -rf /; echo '\\'''`);
    expect(hostile).not.toContain("; rm -rf /; echo ' > /etc/bitacora/token");
  });

  it("honours the paths the hub reported instead of hardcoding them", () => {
    const custom = agentSetupCommand({
      hubURL: "https://hub.example",
      host: { ...host, token_path: "/opt/bitacora/etc/token" },
    });
    expect(custom).toContain("install -d -m 0750 /opt/bitacora/etc");
    expect(custom).toContain("-token-file=/opt/bitacora/etc/token");
  });
});
