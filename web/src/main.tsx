import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import ErrorBoundary from "./components/ErrorBoundary";
import { LocaleProvider } from "./i18n";
import { useTranslation } from "./i18n";
import "./index.css";

function Root() {
  const { t } = useTranslation();

  return (
    <ErrorBoundary
      title={t.unexpectedErrorHeading}
      message={t.unexpectedErrorBody}
      retryLabel={t.reloadPage}
    >
      <App />
    </ErrorBoundary>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <LocaleProvider>
      <Root />
    </LocaleProvider>
  </StrictMode>,
);

if ("serviceWorker" in navigator) {
  window.addEventListener("load", () => navigator.serviceWorker.register("/sw.js"));
}
