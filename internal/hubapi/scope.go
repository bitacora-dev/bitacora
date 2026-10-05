package hubapi

import (
	"net/http"

	"github.com/bitacora-dev/bitacora/internal/hubauth"
	"github.com/bitacora-dev/bitacora/internal/schema"
)

// HostScopeResolver answers the ADR-0023 relation for a human identity: which
// hosts it may view, and which it may operate. It is an interface so the
// installation default can be replaced later by a stored, per-identity
// assignment without touching a single route.
type HostScopeResolver interface {
	HostScope(identity hubauth.Identity) hubauth.HostScope
}

// hostAccess is what the caller of a read route may reach.
//
// human separates the two authentication boundaries the hub has. A device or
// agent token is not a person and gains no human scope (ADR-0023), so it keeps
// the ADR-0014 reach it already had; narrowing it here would silently break
// paired clients on an authorization decision that was never about them.
type hostAccess struct {
	human bool
	scope hubauth.HostScope
}

// canView reports whether this caller may see hostID at all.
func (a hostAccess) canView(hostID string) bool {
	if !a.human {
		return true
	}
	return a.scope.CanView(hostID)
}

// canOperate reports whether this caller may request an ADR-0022 operation on
// hostID. A non-human caller can never reach an action route, so it is
// deliberately not granted the shortcut canView has.
func (a hostAccess) canOperate(hostID string) bool {
	return a.human && a.scope.CanOperate(hostID)
}

// seesEveryHost reports that no per-host filtering is needed for this caller.
func (a hostAccess) seesEveryHost() bool {
	return !a.human || a.scope.SeesEveryHost()
}

// hostAccessFor resolves the caller's reach. It reads the request's own
// identity rather than trusting a middleware to have annotated it, so a route
// that forgets to call this is visibly unguarded instead of quietly
// unrestricted by an absent context value.
func (s *Server) hostAccessFor(r *http.Request) hostAccess {
	if s.Humans == nil {
		return hostAccess{}
	}
	identity, ok := s.Humans.Identity(r)
	if !ok {
		return hostAccess{}
	}
	return hostAccess{human: true, scope: s.resolveScope(identity)}
}

func (s *Server) resolveScope(identity hubauth.Identity) hubauth.HostScope {
	if s.HostScopes != nil {
		return s.HostScopes.HostScope(identity)
	}
	return hubauth.ScopeFor(identity)
}

// hostOutOfViewScope guards a per-host read route. It answers 404 and reports
// true when the caller may not view hostID, which is also the answer for a
// host that does not exist: the two must be indistinguishable, or the error
// code itself tells a stranger which of someone else's servers are real.
func (s *Server) hostOutOfViewScope(w http.ResponseWriter, r *http.Request, hostID string) bool {
	if s.hostAccessFor(r).canView(hostID) {
		return false
	}
	http.Error(w, "host not found", http.StatusNotFound)
	return true
}

// hostOutOfOperateScope guards an ADR-0022 action on one host. ADR-0023 is
// explicit that view does not authorize an operation and that a host the
// identity cannot operate is treated as not visible, so this answers 404 and
// never 403 — including for a host the same identity is allowed to read.
func (s *Server) hostOutOfOperateScope(w http.ResponseWriter, r *http.Request, hostID string) bool {
	if s.hostAccessFor(r).canOperate(hostID) {
		return false
	}
	writeJSONError(w, http.StatusNotFound, "host not found")
	return true
}

// visibleHosts drops the hosts the caller may not view from an aggregate list.
func (s *Server) visibleHosts(r *http.Request, hosts []schema.Host) []schema.Host {
	access := s.hostAccessFor(r)
	if access.seesEveryHost() {
		return hosts
	}
	visible := make([]schema.Host, 0, len(hosts))
	for _, host := range hosts {
		if access.canView(host.ID) {
			visible = append(visible, host)
		}
	}
	return visible
}
