import type { Dictionary } from "./types";
import { es } from "./locales/es";
import { en } from "./locales/en";

// Adding a language is: drop a new locales/<code>.ts satisfying Dictionary,
// then list it here. No component needs to change.
export const locales = ["es", "en"] as const;
export type Locale = (typeof locales)[number];

export const dictionaries: Record<Locale, Dictionary> = { es, en };

// BCP 47 tags for Intl formatting (Date#toLocaleTimeString, etc), keyed by
// the same locale code used for the dictionaries.
export const intlTags: Record<Locale, string> = {
  es: "es-ES",
  en: "en-US",
};
