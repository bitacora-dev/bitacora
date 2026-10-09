package hubauth

// Identity sources and the single local subject ADR-0023 defines. They are
// constants so the authorization rules below and the session the login routes
// mint cannot drift apart on a string literal.
const (
	sourceLocal          = "local"
	sourceOIDC           = "oidc"
	localOperatorSubject = "local:operator"
)

// HostCapabilities is what one identity may do with one host (ADR-0023). The
// zero value grants nothing, so a host nobody related the identity to is
// invisible rather than readable by default.
type HostCapabilities struct {
	// View is permission to read the host at all: its row in /v1/hosts and
	// every per-host read route. Without it the host must look absent.
	View bool
	// Operate is permission to request an ADR-0022 package operation on the
	// host. View never implies it.
	Operate bool
}

// HostScope is the complete, additive ADR-0023 relation between one human
// identity and the hosts it can reach. AllHosts and Hosts add up: a capability
// granted by either one is granted.
type HostScope struct {
	// AllHosts holds the capabilities the identity has on every host,
	// including hosts enrolled after the grant. It is how the installation's
	// single local:operator keeps working as new machines are added.
	AllHosts HostCapabilities
	// Hosts holds capabilities on individually named hosts. It is the shape
	// a future assignment interface will populate; nothing writes it today,
	// which is why there is still no API or UI to assign hosts.
	Hosts map[string]HostCapabilities
}

// CanView reports whether the identity may see hostID at all. A host it cannot
// view must be answered as missing, never as forbidden: a 403 would confirm
// that someone else's server exists, which is the disclosure ADR-0023 rejects.
func (s HostScope) CanView(hostID string) bool {
	return s.AllHosts.View || s.Hosts[hostID].View
}

// CanOperate reports whether the identity may run an ADR-0022 operation on
// hostID.
func (s HostScope) CanOperate(hostID string) bool {
	return s.AllHosts.Operate || s.Hosts[hostID].Operate
}

// SeesEveryHost reports that no read route needs filtering for this identity.
// It exists so the unrestricted case stays a single cheap check instead of a
// per-host lookup over an unbounded host list.
func (s HostScope) SeesEveryHost() bool {
	return s.AllHosts.View
}

// ScopeFor returns the installation's relation for an authenticated human
// identity. There is no relation store yet — ADR-0023 keeps account creation
// and host assignment out of this delivery — so the result is derived from the
// identity alone.
//
// Both sources that produce a human session today are whole-installation
// operators, and they stay that way:
//
//   - local:operator is the single local subject ADR-0023 defines, granted
//     view and operate on all hosts by the installation itself.
//   - An OIDC subject is a person the operator's own IdP vouched for. ADR-0019
//     makes that session the human boundary and the hub holds no per-subject
//     authorization for it. Returning an empty scope here would not restrict
//     such a deployment, it would blank its dashboard, which is a regression
//     no ADR asked for.
//
// Agent and device tokens never reach this function: they are not human
// identities and ADR-0023 keeps them separate from human scope.
func ScopeFor(identity Identity) HostScope {
	switch {
	case identity.Source == sourceLocal && identity.Subject == localOperatorSubject:
		return HostScope{AllHosts: HostCapabilities{View: true, Operate: true}}
	case identity.Source == sourceOIDC && identity.Subject != "":
		return HostScope{AllHosts: HostCapabilities{View: true, Operate: true}}
	default:
		return HostScope{}
	}
}
