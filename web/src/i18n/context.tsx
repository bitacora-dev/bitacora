import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { dictionaries, intlTags, type Locale } from "./dictionaries";

// No language selector in the UI yet (not the focus of this pass) — this is
// where a future one would call setLocale.
const DEFAULT_LOCALE: Locale = "es";

interface LocaleContextValue {
  locale: Locale;
  setLocale: (locale: Locale) => void;
}

const LocaleContext = createContext<LocaleContextValue | null>(null);

export function LocaleProvider({ children }: { children: ReactNode }) {
  const [locale, setLocale] = useState<Locale>(DEFAULT_LOCALE);

  useEffect(() => {
    document.documentElement.lang = locale;
  }, [locale]);

  const value = useMemo(() => ({ locale, setLocale }), [locale]);

  return <LocaleContext.Provider value={value}>{children}</LocaleContext.Provider>;
}

export function useTranslation() {
  const ctx = useContext(LocaleContext);
  if (!ctx) throw new Error("useTranslation must be used within a LocaleProvider");
  return {
    t: dictionaries[ctx.locale],
    locale: ctx.locale,
    intlTag: intlTags[ctx.locale],
    setLocale: ctx.setLocale,
  };
}
