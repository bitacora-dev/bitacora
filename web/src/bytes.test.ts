import { describe, expect, it } from "vitest";
import { formatBytes } from "./bytes";

describe("formatBytes", () => {
  const cases = [
    { bytes: 2_199_023_255_552, en: "2.2 TB", es: "2,2 TB" },
    { bytes: 126_800_000_000, en: "126.8 GB", es: "126,8 GB" },
    { bytes: 1_125_000_000, en: "1.1 GB", es: "1,1 GB" },
    { bytes: 0, en: "0 byte", es: "0 B" },
  ] as const;

  for (const { bytes, en, es } of cases) {
    it(`formats ${bytes} bytes with decimal units in English and Spanish`, () => {
      expect(formatBytes(bytes, "en")).toBe(en);
      expect(formatBytes(bytes, "es")).toBe(es);
    });
  }

  it("shows an explicit placeholder for non-numeric input", () => {
    expect(formatBytes("not a byte count", "en")).toBe("—");
    expect(formatBytes("not a byte count", "es")).toBe("—");
  });
});
