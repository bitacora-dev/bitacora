package hubauth

// HostScope represents the future per-host authorization relation from
// ADR-0023. It is intentionally only a model in this change: existing read and
// action routes retain their current behavior until their filtering migration
// can be delivered as one complete, non-leaking unit.
type HostScope struct {
	AllHosts bool
	View     bool
	Operate  bool
}

// ScopeFor returns the installation's initial relation. No other local subject
// exists, and OIDC identities do not acquire local permissions implicitly.
func ScopeFor(identity Identity) HostScope {
	if identity.Source == "local" && identity.Subject == "local:operator" {
		return HostScope{AllHosts: true, View: true, Operate: true}
	}
	return HostScope{}
}
