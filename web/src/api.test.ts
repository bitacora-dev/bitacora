import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchEventHistory, fetchHosts } from "./api";

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

describe("fetchEventHistory", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("sends the device token, explicit range, filters and page", async () => {
    vi.stubGlobal("localStorage", { getItem: vi.fn().mockReturnValue("device-token") });
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ events: [] }), { status: 200 }));
    vi.stubGlobal("fetch", fetch);

    await fetchEventHistory("host a", { from: "2026-01-01T00:00:00Z", to: "2026-01-02T00:00:00Z", severity: "error", type: "kernel.segfault", limit: 50, offset: 100 });

    expect(fetch).toHaveBeenCalledWith(
      "/v1/events?host_id=host+a&from=2026-01-01T00%3A00%3A00Z&to=2026-01-02T00%3A00%3A00Z&severity=error&type=kernel.segfault&limit=50&offset=100",
      { headers: { Authorization: "Bearer device-token" } },
    );
  });
});
