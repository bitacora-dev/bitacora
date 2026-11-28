package main

import (
	"fmt"
	"io"
	"time"

	"github.com/bitacora-dev/bitacora/internal/hubauth"
)

// announceSetup is the hub's half of ADR-0025's installation. When the hub has
// no login source at all it issues a fresh install token and writes it, once,
// to out — the process's stderr, which under systemd is the journal and in a
// container is `docker logs`: both already require local administrative
// access to read. Only the token's hash is persisted, so every start without
// a credential issues a new one and the previous one stops working.
//
// A credential that exists but is disabled gets no install token: an install
// token can never overwrite a credential. The operator is pointed at the
// local recovery command instead.
func announceSetup(auth *hubauth.Authenticator, local *hubauth.LocalStore, ttl time.Duration, out io.Writer) error {
	if !auth.SetupRequired() {
		return nil
	}
	if local.Initialized() {
		fmt.Fprintln(out, "bitacora-hub: no login source is available: the local credential is disabled and OIDC is not configured.")
		fmt.Fprintln(out, "bitacora-hub: the dashboard and data API stay closed. Run `bitacora-hub auth local recover` on this server to get a one-time recovery token for /auth/setup.")
		return nil
	}
	token, expiresAt, err := local.IssueSetupToken(hubauth.SetupInstall, ttl)
	if err != nil {
		return fmt.Errorf("issuing the install token: %w", err)
	}
	fmt.Fprintln(out, "bitacora-hub: ================================================================")
	fmt.Fprintln(out, "bitacora-hub: no login source is configured; the dashboard and data API are closed until setup completes (ADR-0025).")
	fmt.Fprintf(out, "bitacora-hub: open /auth/setup on this hub and enter this one-time install token (valid until %s):\n", expiresAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(out, "bitacora-hub:     %s\n", token)
	fmt.Fprintln(out, "bitacora-hub: it is shown only here. If it expires, restart the hub or run `bitacora-hub auth local recover`.")
	fmt.Fprintln(out, "bitacora-hub: ================================================================")
	return nil
}
