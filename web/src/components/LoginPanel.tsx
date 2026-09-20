import { FormEvent, useEffect, useState } from "react";
import { useTranslation } from "../i18n";

interface LoginSources {
  local_enabled: boolean;
  oidc_enabled: boolean;
}

interface LoginError {
  error?: string;
  locked_until?: string;
}

function returnTo(): string {
  const value = new URLSearchParams(window.location.search).get("return_to") ?? "/";
  return value.startsWith("/") && !value.startsWith("//") ? value : "/";
}

export default function LoginPanel() {
  const { t, intlTag } = useTranslation();
  const [sources, setSources] = useState<LoginSources | null>(null);
  const [recovery, setRecovery] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<LoginError | null>(null);
  const expired = new URLSearchParams(window.location.search).get("expired") === "1";

  useEffect(() => {
    fetch("/auth/me")
      .then(async (response) => response.json() as Promise<LoginSources>)
      .then(setSources)
      .catch(() => setSources({ local_enabled: false, oidc_enabled: false }));
  }, []);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const body = {
      password: String(form.get("password") ?? ""),
      ...(recovery ? { recovery_code: String(form.get("recovery_code") ?? "") } : { totp: String(form.get("totp") ?? "") }),
      return_to: returnTo(),
    };
    setSubmitting(true);
    setError(null);
    try {
      const response = await fetch("/auth/local/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!response.ok) {
        setError(await response.json() as LoginError);
        return;
      }
      window.location.assign(returnTo());
    } catch {
      setError({ error: "request_failed" });
    } finally {
      setSubmitting(false);
    }
  };

  if (sources === null) return <main className="auth-shell"><p className="loading-text">{t.loading}</p></main>;

  return (
    <main className="auth-shell">
      <section className="auth-panel login-panel" aria-labelledby="sign-in-heading">
        <div className="brand-lockup"><img className="brand-mark" src="/bitacora-logo.png" alt="" aria-hidden="true" /><h1>{t.brand}</h1></div>
        <div><h2 id="sign-in-heading">{t.signInHeading}</h2><p>{t.signInIntro}</p></div>
        {expired && <div className="notice-panel" role="status">{t.sessionExpired}</div>}
        {error && <div className="error-panel" role="alert">{error.locked_until ? t.accountLockedUntil(new Date(error.locked_until).toLocaleTimeString(intlTag)) : t.invalidCredentials}</div>}
        {sources.local_enabled && <form className="host-form" onSubmit={submit}>
          <label htmlFor="login-password">{t.passwordLabel}</label>
          <input id="login-password" name="password" type="password" autoComplete="current-password" required />
          {recovery ? <><label htmlFor="login-recovery-code">{t.recoveryCodeLabel}</label><input id="login-recovery-code" name="recovery_code" autoComplete="one-time-code" required /><p>{t.recoveryCodeHint}</p></> : <><label htmlFor="login-totp">{t.authenticationCodeLabel}</label><input id="login-totp" name="totp" inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]*" required /><p>{t.authenticationCodeHint}</p></>}
          <button className="link-button" type="button" onClick={() => setRecovery((value) => !value)}>{recovery ? t.useAuthenticatorCode : t.useRecoveryCode}</button>
          <button className="primary-button" type="submit" disabled={submitting}>{submitting ? t.signingIn : t.signInButton}</button>
        </form>}
        {sources.oidc_enabled && <a className="secondary-button" href={`/auth/oidc/login?return_to=${encodeURIComponent(returnTo())}`}>{t.signInWithOIDC}</a>}
        {!sources.local_enabled && !sources.oidc_enabled && <div className="error-panel" role="alert">{t.loginUnavailable}</div>}
      </section>
    </main>
  );
}
