// Client side of the ADR-0025 first-run and recovery setup. The server is the
// authority on every rule here: it validates the token, enforces the password
// length and hashes with Argon2id. This module only gives early hints.

export const MIN_PASSWORD_LENGTH = 12;

export type PasswordStrength = "too_short" | "weak" | "fair" | "strong";

// passwordStrength is a hint, not a policy. Length dominates because it is
// what actually resists guessing; character variety only nudges the rating.
export function passwordStrength(password: string, username = ""): PasswordStrength {
  const length = [...password].length;
  if (length < MIN_PASSWORD_LENGTH) return "too_short";
  if (username !== "" && password.toLowerCase() === username.trim().toLowerCase()) return "weak";
  const classes = [/[a-z]/, /[A-Z]/, /[0-9]/, /[^A-Za-z0-9]/].filter((pattern) => pattern.test(password)).length;
  const distinct = new Set(password).size;
  if (distinct < 5) return "weak";
  if (length >= 20 || (length >= 16 && classes >= 2) || (length >= 14 && classes >= 3)) return "strong";
  if (classes >= 2 || length >= 16) return "fair";
  return "weak";
}

export type SetupErrorCode =
  | "invalid_setup_token"
  | "invalid_username"
  | "password_too_short"
  | "password_too_long"
  | "password_matches_username"
  | "invalid_code"
  | "setup_restart_required"
  | "rate_limited"
  | "setup_failed";

export interface SetupBegin {
  enrollment_id: string;
  totp_secret: string;
  otpauth_uri: string;
  expires_at: string;
}

export type SetupResult<T> = { ok: true; value: T } | { ok: false; error: SetupErrorCode };

const knownErrors: readonly SetupErrorCode[] = [
  "invalid_setup_token",
  "invalid_username",
  "password_too_short",
  "password_too_long",
  "password_matches_username",
  "invalid_code",
  "setup_restart_required",
];

// setupErrorCode maps a server answer onto the codes the screen can explain.
// Anything unexpected becomes setup_failed rather than leaking raw text.
export function setupErrorCode(status: number, body: unknown): SetupErrorCode {
  if (status === 429) return "rate_limited";
  const error = typeof body === "object" && body !== null ? (body as { error?: unknown }).error : undefined;
  return typeof error === "string" && (knownErrors as readonly string[]).includes(error) ? (error as SetupErrorCode) : "setup_failed";
}

// groupSecret splits a base32 secret into blocks of four for manual entry.
export function groupSecret(secret: string): string {
  return secret.match(/.{1,4}/g)?.join(" ") ?? secret;
}

async function postSetup<T>(path: string, body: Record<string, string>): Promise<SetupResult<T>> {
  try {
    const response = await fetch(path, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    let payload: unknown = null;
    try {
      payload = await response.json();
    } catch {
      payload = null;
    }
    if (!response.ok) return { ok: false, error: setupErrorCode(response.status, payload) };
    return { ok: true, value: payload as T };
  } catch {
    return { ok: false, error: "setup_failed" };
  }
}

export function beginSetup(token: string, username: string, password: string): Promise<SetupResult<SetupBegin>> {
  return postSetup<SetupBegin>("/auth/setup/begin", { token, username, password });
}

export async function completeSetup(token: string, enrollmentID: string, totp: string): Promise<SetupResult<string[]>> {
  const result = await postSetup<{ recovery_codes: string[] }>("/auth/setup/complete", { token, enrollment_id: enrollmentID, totp });
  return result.ok ? { ok: true, value: result.value.recovery_codes } : result;
}
