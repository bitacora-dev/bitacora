import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchHosts } from "./api";

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
