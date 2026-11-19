// What GET /v1/auth/session reports about the hub's human authentication
// boundary. auth_enabled distinguishes "this hub has no login" from
// "configured, and you are not signed in" — the two states that looked
// identical while the endpoint answered 404.
export interface AuthSession {
  auth_enabled: boolean;
  authenticated: boolean;
  local_enabled: boolean;
  oidc_enabled: boolean;
  local_pending_initialization: boolean;
}

export type LoginNotice = "none" | "pending_initialization" | "unavailable";

export interface LoginScreen {
  localForm: boolean;
  oidcLink: boolean;
  notice: LoginNotice;
}

// authSessionFallback is what an unreachable or unparseable endpoint means:
// nothing to offer. It deliberately reports no login route rather than
// assuming the hub is open.
export function authSessionFallback(): AuthSession {
  return {
    auth_enabled: false,
    authenticated: false,
    local_enabled: false,
    oidc_enabled: false,
    local_pending_initialization: false,
  };
}

// loginScreen decides what the unauthenticated screen shows. The pending case
// shows no form on purpose: ADR-0023 keeps credential creation on the server's
// TTY, so a password field here would be an enrolment this design refuses.
export function loginScreen(session: AuthSession): LoginScreen {
  if (session.local_pending_initialization) {
    return { localForm: false, oidcLink: false, notice: "pending_initialization" };
  }
  const localForm = session.auth_enabled && session.local_enabled;
  const oidcLink = session.auth_enabled && session.oidc_enabled;
  return { localForm, oidcLink, notice: localForm || oidcLink ? "none" : "unavailable" };
}

export async function fetchAuthSession(): Promise<AuthSession> {
  try {
    const response = await fetch("/v1/auth/session");
    if (!response.ok) return authSessionFallback();
    return { ...authSessionFallback(), ...(await response.json() as Partial<AuthSession>) };
  } catch {
    return authSessionFallback();
  }
}
