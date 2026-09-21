import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchEventHistory, fetchHosts, fetchInventory, fetchLogHistory, hasInstant } from "./api";

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

describe("fetchLogHistory", () => {
	afterEach(() => vi.unstubAllGlobals());
	it("sends an explicit range, efficient filters and page", async () => {
		vi.stubGlobal("localStorage", { getItem: vi.fn().mockReturnValue("device-token") });
		const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ entries: [] }), { status: 200 }));
		vi.stubGlobal("fetch", fetch);
		await fetchLogHistory("host a", { from: "2026-01-01T00:00:00Z", to: "2026-01-02T00:00:00Z", text: "failed", source: "journald", unit: "nginx.service", limit: 50, offset: 100 });
		expect(fetch).toHaveBeenCalledWith("/v1/logs?host_id=host+a&from=2026-01-01T00%3A00%3A00Z&to=2026-01-02T00%3A00%3A00Z&limit=50&offset=100&text=failed&source=journald&unit=nginx.service", { headers: { Authorization: "Bearer device-token" } });
	});

	it("narrows the page to the single block an event's log_refs names", async () => {
		vi.stubGlobal("localStorage", { getItem: vi.fn().mockReturnValue("device-token") });
		const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ entries: [] }), { status: 200 }));
		vi.stubGlobal("fetch", fetch);
		await fetchLogHistory("host-a", { from: "2026-01-01T00:00:00Z", to: "2026-01-02T00:00:00Z", block: "block a", limit: 50, offset: 0 });
		expect(fetch).toHaveBeenCalledWith("/v1/logs?host_id=host-a&from=2026-01-01T00%3A00%3A00Z&to=2026-01-02T00%3A00%3A00Z&limit=50&offset=0&block=block+a", { headers: { Authorization: "Bearer device-token" } });
	});
});

describe("hasInstant", () => {
  it("rejects the zero instant Go sends for a timestamp `omitempty` never drops", () => {
    expect(hasInstant("0001-01-01T00:00:00Z")).toBe(false);
    expect(hasInstant("")).toBe(false);
    expect(hasInstant(undefined)).toBe(false);
    expect(hasInstant("2026-09-19T11:31:18Z")).toBe(true);
  });
});
