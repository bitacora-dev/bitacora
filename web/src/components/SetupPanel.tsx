import QRCode from "qrcode";
import { FormEvent, useEffect, useState } from "react";
import { useTranslation } from "../i18n";
import { authSessionFallback, fetchAuthSession, type AuthSession } from "../login";
import {
  beginSetup,
  completeSetup,
  groupSecret,
  MIN_PASSWORD_LENGTH,
  passwordStrength,
  type SetupBegin,
  type SetupErrorCode,
} from "../setup";

type Step =
  | { kind: "credentials" }
  | { kind: "totp"; token: string; begin: SetupBegin; qr: string | null }
  | { kind: "recovery"; codes: string[] };

// SetupPanel is the ADR-0025 web setup: it redeems an install or recovery
// token that only the server's console or a local command can deliver. It
// never asks the hub to issue, resend or reveal a token.
export default function SetupPanel() {
  const { t } = useTranslation();
  const [session, setSession] = useState<AuthSession | null>(null);
  const [step, setStep] = useState<Step>({ kind: "credentials" });
  const [password, setPassword] = useState("");
  const [username, setUsername] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<SetupErrorCode | "password_mismatch" | null>(null);
  const [stored, setStored] = useState(false);

  useEffect(() => {
    fetchAuthSession().then(setSession).catch(() => setSession(authSessionFallback()));
  }, []);

  const errorText = (code: SetupErrorCode | "password_mismatch"): string => {
    switch (code) {
      case "invalid_setup_token": return t.setupErrorInvalidToken;
      case "invalid_username": return t.setupErrorInvalidUsername;
      case "password_too_short": return t.setupPasswordTooShort(MIN_PASSWORD_LENGTH);
      case "password_too_long": return t.setupErrorPasswordTooLong;
      case "password_matches_username": return t.setupErrorPasswordMatchesUsername;
      case "password_mismatch": return t.setupErrorPasswordMismatch;
      case "invalid_code": return t.setupErrorInvalidCode;
      case "setup_restart_required": return t.setupErrorRestartRequired;
      case "rate_limited": return t.setupErrorRateLimited;
      case "setup_state_read_only": return t.setupErrorStateReadOnly;
      default: return t.setupErrorFailed;
    }
  };

  const submitCredentials = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const token = String(form.get("token") ?? "").trim();
    if (password !== String(form.get("confirm_password") ?? "")) {
      setError("password_mismatch");
      return;
    }
    setSubmitting(true);
    setError(null);
    const result = await beginSetup(token, username, password);
    setSubmitting(false);
    if (!result.ok) {
      setError(result.error);
      return;
    }
    // The password has been hashed by the hub; drop the plaintext here too.
    setPassword("");
    let qr: string | null = null;
    try {
      qr = await QRCode.toDataURL(result.value.otpauth_uri);
    } catch {
      qr = null;
    }
    setStep({ kind: "totp", token, begin: result.value, qr });
  };

  const submitCode = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (step.kind !== "totp") return;
    const form = new FormData(event.currentTarget);
    setSubmitting(true);
    setError(null);
    const result = await completeSetup(step.token, step.begin.enrollment_id, String(form.get("totp") ?? "").trim());
    setSubmitting(false);
    if (!result.ok) {
      setError(result.error);
      if (result.error === "setup_restart_required" || result.error === "invalid_setup_token") setStep({ kind: "credentials" });
      return;
    }
    setStep({ kind: "recovery", codes: result.value });
  };

  if (session === null) return <main className="auth-shell"><p className="loading-text">{t.loading}</p></main>;

  // A configured hub does not advertise a pending recovery token, so outside
  // setup mode the form is always offered and the hub judges the token.
  const canRedeem = !session.setup_required || session.setup_available || step.kind !== "credentials";
  const strength = passwordStrength(password, username);

  return (
    <main className="auth-shell">
      <section className="auth-panel login-panel" aria-labelledby="setup-heading">
        <div className="brand-lockup"><img className="brand-mark" src="/bitacora-logo.png" alt="" aria-hidden="true" /><h1>{t.brand}</h1></div>
        <div><h2 id="setup-heading">{t.setupHeading}</h2><p>{session.setup_required ? t.setupIntroInstall : t.setupIntroRecovery}</p></div>
        {error && <div className="error-panel" role="alert">{errorText(error)}</div>}

        {!canRedeem && <div className="notice-panel" role="status">{t.setupNoTokenPending}</div>}

        {canRedeem && step.kind === "credentials" && <form className="host-form" onSubmit={submitCredentials}>
          <label htmlFor="setup-token">{t.setupTokenLabel}</label>
          <input id="setup-token" name="token" autoComplete="off" spellCheck={false} required />
          <p>{t.setupTokenHint}</p>
          <label htmlFor="setup-username">{t.usernameLabel}</label>
          <input id="setup-username" name="username" autoComplete="username" maxLength={64} required value={username} onChange={(event) => setUsername(event.target.value)} />
          <p>{t.setupUsernameHint}</p>
          <label htmlFor="setup-password">{t.setupNewPasswordLabel}</label>
          <input id="setup-password" name="password" type="password" autoComplete="new-password" minLength={MIN_PASSWORD_LENGTH} required value={password} onChange={(event) => setPassword(event.target.value)} />
          <p aria-live="polite">{password === "" ? t.setupPasswordTooShort(MIN_PASSWORD_LENGTH) : t.setupPasswordStrength(strength)}</p>
          <label htmlFor="setup-confirm-password">{t.setupConfirmPasswordLabel}</label>
          <input id="setup-confirm-password" name="confirm_password" type="password" autoComplete="new-password" required />
          <button className="primary-button" type="submit" disabled={submitting || strength === "too_short"}>{submitting ? t.setupWorking : t.setupContinue}</button>
        </form>}

        {step.kind === "totp" && <form className="host-form" onSubmit={submitCode}>
          <p>{t.setupTotpIntro}</p>
          {step.qr && <div className="qr-layout"><img src={step.qr} alt={t.setupQrAlt} /></div>}
          <label htmlFor="setup-secret">{t.setupManualSecretLabel}</label>
          <input id="setup-secret" readOnly value={groupSecret(step.begin.totp_secret)} spellCheck={false} />
          <label htmlFor="setup-totp">{t.authenticationCodeLabel}</label>
          <input id="setup-totp" name="totp" inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]*" maxLength={6} required />
          <p>{t.authenticationCodeHint}</p>
          <button className="primary-button" type="submit" disabled={submitting}>{submitting ? t.setupWorking : t.setupConfirmCode}</button>
        </form>}

        {step.kind === "recovery" && <div className="host-form">
          <p>{t.setupRecoveryIntro}</p>
          <ol className="setup-recovery-codes">{step.codes.map((code) => <li key={code}><code>{code}</code></li>)}</ol>
          <label className="setup-confirm-row"><input type="checkbox" checked={stored} onChange={(event) => setStored(event.target.checked)} /> {t.setupRecoveryStored}</label>
          <button className="primary-button" type="button" disabled={!stored} onClick={() => window.location.assign("/")}>{t.setupOpenDashboard}</button>
        </div>}
      </section>
    </main>
  );
}
