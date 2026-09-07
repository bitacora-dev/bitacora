import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchHosts, fetchInventory } from "./api";

describe("fetchHosts", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("normalizes a legacy null response to an empty host list", async () => {
    vi.stubGlobal("localStorage", { getItem: vi.fn().mockReturnValue(null) });
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("null", { status: 200 })));

    await expect(fetchHosts()).resolves.toEqual([]);
  });
});

describe("fetchInventory", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("requests a host and kind with the device token", async () => {
    vi.stubGlobal("localStorage", { getItem: vi.fn().mockReturnValue("device-token") });
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ host_id: "host a", kind: "disk", items: [] }), { status: 200 }));
    vi.stubGlobal("fetch", fetch);

    await expect(fetchInventory("host a", "disk")).resolves.toMatchObject({ kind: "disk", items: [] });
    expect(fetch).toHaveBeenCalledWith("/v1/inventory?host_id=host%20a&kind=disk", {
      headers: { Authorization: "Bearer device-token" },
    });
  });

  it("treats an unreported optional inventory as absent", async () => {
    vi.stubGlobal("localStorage", { getItem: vi.fn().mockReturnValue(null) });
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("missing", { status: 404 })));

    await expect(fetchInventory("host-a", "package_update")).resolves.toBeNull();
  });
});
