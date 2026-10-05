package hubauth

import "testing"

func TestScopeForGrantsTheInitialLocalOperatorEveryHost(t *testing.T) {
	scope := ScopeFor(Identity{Source: sourceLocal, Subject: localOperatorSubject})
	if !scope.SeesEveryHost() || !scope.CanView("any-host") || !scope.CanOperate("any-host") {
		t.Fatalf("local operator scope = %#v, want view and operate on every host", scope)
	}
}

// An OIDC subject is vouched for by the operator's own IdP and ADR-0019 holds
// no per-subject authorization in the hub. Granting it nothing here would not
// restrict such a deployment, it would blank its dashboard.
func TestScopeForKeepsOIDCDeploymentsReadable(t *testing.T) {
	scope := ScopeFor(Identity{Source: sourceOIDC, Subject: "person@example.test"})
	if !scope.SeesEveryHost() || !scope.CanOperate("any-host") {
		t.Fatalf("OIDC scope = %#v, want the ADR-0019 boundary to keep its reach", scope)
	}
}

func TestScopeForGrantsNothingToAnIdentityNoLoginRouteProduces(t *testing.T) {
	for _, identity := range []Identity{
		{},
		{Source: sourceOIDC},
		{Source: sourceLocal, Subject: "local:someone-else"},
		{Source: "made-up", Subject: localOperatorSubject},
	} {
		scope := ScopeFor(identity)
		if scope.SeesEveryHost() || scope.CanView("any-host") || scope.CanOperate("any-host") {
			t.Fatalf("scope for %#v = %#v, want nothing", identity, scope)
		}
	}
}

// ADR-0023 is explicit that view never authorizes an operation.
func TestHostScopeKeepsViewAndOperateIndependentPerHost(t *testing.T) {
	scope := HostScope{Hosts: map[string]HostCapabilities{
		"host-a": {View: true},
		"host-b": {View: true, Operate: true},
	}}
	if scope.SeesEveryHost() {
		t.Fatal("a per-host scope claimed it sees every host")
	}
	if !scope.CanView("host-a") || scope.CanOperate("host-a") {
		t.Fatal("view on host-a leaked operate")
	}
	if !scope.CanView("host-b") || !scope.CanOperate("host-b") {
		t.Fatal("host-b lost a capability it was granted")
	}
	if scope.CanView("host-c") || scope.CanOperate("host-c") {
		t.Fatal("an unlisted host was reachable")
	}
}

// AllHosts and Hosts are additive, which is how a later per-host grant can be
// added to an identity without first revoking anything.
func TestHostScopeAddsAllHostsAndPerHostCapabilities(t *testing.T) {
	scope := HostScope{
		AllHosts: HostCapabilities{View: true},
		Hosts:    map[string]HostCapabilities{"host-b": {Operate: true}},
	}
	if !scope.CanView("host-a") || scope.CanOperate("host-a") {
		t.Fatal("all-hosts view did not apply, or it implied operate")
	}
	if !scope.CanView("host-b") || !scope.CanOperate("host-b") {
		t.Fatal("per-host operate did not add to the all-hosts view")
	}
}
