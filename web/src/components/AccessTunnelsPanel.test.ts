import { describe, expect, it } from "vitest";
import type { Inventory } from "../api";
import { activeTunnelCount, readTunnel, readTunnels } from "./AccessTunnelsPanel";

function inventory(items: Inventory["items"]): Inventory {
  return { host_id: "host-a", kind: "vpn_tunnel", reported_at: "2026-09-21T12:00:00Z", schema: 1, items };
}

describe("readTunnel", () => {
  it("reads a WireGuard peer row", () => {
    expect(
      readTunnel({
        id: "wg0/abcdefghijkl…",
        name: "wg0",
        attrs: { protocol: "wireguard", peer: "abcdefghijkl…", endpoint: "203.0.113.5:51820", active: "true", last_handshake: "2026-09-21T11:59:10Z" },
      }),
    ).toEqual({
      id: "wg0/abcdefghijkl…",
      name: "wg0",
      protocol: "wireguard",
      active: true,
      peer: "abcdefghijkl…",
      endpoint: "203.0.113.5:51820",
      lastHandshake: "2026-09-21T11:59:10Z",
      state: null,
    });
  });

  it("reads the Tailscale row, which carries a backend state and no peer", () => {
    const tunnel = readTunnel({ id: "tailscale", name: "tailscale", attrs: { protocol: "tailscale", active: "true", state: "Running" } });
    expect(tunnel.protocol).toBe("tailscale");
    expect(tunnel.state).toBe("Running");
    expect(tunnel.peer).toBeNull();
    expect(tunnel.endpoint).toBeNull();
  });

  // `wg show dump` writes the literal "(none)" for a peer it has never
  // reached. Rendering that string would dress an absence up as an address.
  it("treats the WireGuard placeholder endpoint as absent", () => {
    expect(readTunnel({ id: "wg0/peer", name: "wg0", attrs: { protocol: "wireguard", endpoint: "(none)" } }).endpoint).toBeNull();
  });

  // The agent writes an empty string, not a zero instant, for a peer that has
  // never completed a handshake.
  it("treats an empty handshake as never, not as an instant", () => {
    expect(readTunnel({ id: "wg0/peer", name: "wg0", attrs: { protocol: "wireguard", last_handshake: "" } }).lastHandshake).toBeNull();
  });

  it("does not read a missing active attribute as a down tunnel", () => {
    expect(readTunnel({ id: "wg0/peer", name: "wg0", attrs: { protocol: "wireguard" } }).active).toBeNull();
  });

  it("keeps an unrecognised protocol out of the two known shapes", () => {
    expect(readTunnel({ id: "x", name: "x", attrs: { protocol: "zerotier" } }).protocol).toBe("unknown");
  });
});

describe("readTunnels", () => {
  // The network collector emits the snapshot with a nil slice when the
  // privileged helper has written nothing, so `items` arrives as null.
  it("survives the null item list an empty snapshot produces", () => {
    expect(readTunnels({ ...inventory([]), items: null as unknown as Inventory["items"] })).toEqual([]);
  });

  it("returns nothing for a host that never reported tunnels", () => {
    expect(readTunnels(null)).toEqual([]);
  });
});

describe("activeTunnelCount", () => {
  it("counts only the tunnels that reported themselves active", () => {
    const tunnels = readTunnels(inventory([
      { id: "wg0/a", name: "wg0", attrs: { protocol: "wireguard", active: "true" } },
      { id: "wg0/b", name: "wg0", attrs: { protocol: "wireguard", active: "false" } },
      { id: "wg0/c", name: "wg0", attrs: { protocol: "wireguard" } },
    ]));
    expect(activeTunnelCount(tunnels)).toBe(1);
  });
});
