// Package vpnhelper implements the bitacora-vpn helper's logic
// (ADR-0005, ADR-0016): read WireGuard and Tailscale tunnel status, both
// of which need privilege this project's agent deliberately doesn't have.
// Command execution is injected so this is testable without either
// program installed.
package vpnhelper

import "context"

// CommandRunner runs one command from the closed list this helper is
// allowed to call (ADR-0012: "una lista cerrada de comandos de consulta
// [...] sin parámetros procedentes de datos externos") and returns its raw
// stdout. Both commands this package calls take no arguments derived from
// anything external — see Run.
type CommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Result is the "data" payload written into the spool entry. Both fields
// are the raw, unparsed command output — parsing WireGuard's tab-separated
// dump format and Tailscale's JSON happens unprivileged, in
// internal/collector/network, not here. This helper's only job is
// capturing output safely; interpreting it is somebody else's problem,
// same separation bitacora-smart uses for smartctl's JSON.
type Result struct {
	// WireguardDump is `wg show all dump`'s raw stdout — empty if wg isn't
	// installed or no interfaces are configured, not an error.
	WireguardDump string `json:"wireguard_dump,omitempty"`
	// TailscaleStatusJSON is `tailscale status --json`'s raw stdout.
	TailscaleStatusJSON string `json:"tailscale_status_json,omitempty"`
}

// Run calls both commands. Either one being absent (the daemon isn't
// installed, or isn't running) is reported as a non-fatal error string,
// not a reason to fail the whole run — a host might only use one of the
// two, or neither.
func Run(ctx context.Context, run CommandRunner) (Result, []string) {
	var result Result
	var errs []string

	if out, err := run(ctx, "wg", "show", "all", "dump"); err == nil {
		result.WireguardDump = string(out)
	} else {
		errs = append(errs, "wg show all dump: "+err.Error())
	}

	if out, err := run(ctx, "tailscale", "status", "--json"); err == nil {
		result.TailscaleStatusJSON = string(out)
	} else {
		errs = append(errs, "tailscale status --json: "+err.Error())
	}

	return result, errs
}
