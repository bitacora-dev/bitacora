import { describe, expect, it } from "vitest";
import { dictionaries, locales } from "./dictionaries";

function keyPaths(value: unknown, prefix = ""): string[] {
  if (typeof value !== "object" || value === null) {
    return [prefix];
  }
  return Object.entries(value).flatMap(([key, v]) => keyPaths(v, prefix ? `${prefix}.${key}` : key));
}

describe("i18n dictionaries", () => {
  it("expose the exact same set of keys in every locale", () => {
    const [first, ...rest] = locales.map((locale) => keyPaths(dictionaries[locale]).sort());
    for (const other of rest) {
      expect(other).toEqual(first);
    }
  });
});
