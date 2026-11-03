package runtime

import (
	"context"
	"errors"
	"testing"
)

func TestRuleBasedPolicyCheckResolvedConnection(t *testing.T) {
	allowlist := NewRuleBasedPolicy()
	allowlist.AllowHost(ProtocolTCP, DirectionOutbound, "api.example.com")

	ordered := NewRuleBasedPolicy()
	ordered.DenyHost(ProtocolAny, DirectionAny, "10.0.0.0/8")
	ordered.AllowHost(ProtocolTCP, DirectionOutbound, "api.example.com")

	cases := []struct {
		name         string
		policy       *RuleBasedPolicy
		hostname, ip string
		allowed      bool
	}{
		{"allowlisted name with unlisted ip", allowlist, "api.example.com", "203.0.113.1", true},
		{"unlisted name", allowlist, "other.example.com", "203.0.113.1", false},
		{"listed name resolving to denied ip", ordered, "api.example.com", "10.0.0.1", false},
		{"no private networks denies by ip", CommonPolicies.NoPrivateNetworks(), "any.example.com", "169.254.169.254", false},
		{"no private networks denies ipv6", CommonPolicies.NoPrivateNetworks(), "any.example.com", "fd00::1", false},
		{"no private networks allows public", CommonPolicies.NoPrivateNetworks(), "any.example.com", "203.0.113.1", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.policy.CheckResolvedConnection(ProtocolTCP, DirectionOutbound, tc.hostname, tc.ip, 443)
			if (err == nil) != tc.allowed {
				t.Fatalf("err = %v, allowed = %v", err, tc.allowed)
			}
			if err != nil && !errors.Is(err, ErrConnectionDenied) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestHostListPolicyCheckResolvedConnection(t *testing.T) {
	p := NewHostListPolicy([]string{"*.example.com"}, []string{"169.254.169.254"})
	if err := p.CheckResolvedConnection(ProtocolTCP, DirectionOutbound, "api.example.com", "203.0.113.1", 443); err != nil {
		t.Fatal(err)
	}
	if err := p.CheckResolvedConnection(ProtocolTCP, DirectionOutbound, "api.example.com", "169.254.169.254", 443); err == nil {
		t.Fatal("denied ip was allowed")
	}
	if err := p.CheckResolvedConnection(ProtocolTCP, DirectionOutbound, "other.test", "203.0.113.1", 443); err == nil {
		t.Fatal("unlisted host was allowed")
	}
}

type stubSocket struct {
	NoOpTCPSocket
	connected string
}

func (s *stubSocket) Connect(_ context.Context, address string, _ int) error {
	s.connected = address
	return nil
}

func TestFilteredSocketConnectResolvedPinsIP(t *testing.T) {
	stub := &stubSocket{}
	socket := &FilteredTCPSocket{underlying: stub, policy: NewAllowAllPolicy()}
	if err := socket.ConnectResolved(context.Background(), "api.example.com", "203.0.113.1", 443); err != nil {
		t.Fatal(err)
	}
	if stub.connected != "203.0.113.1" {
		t.Fatalf("connected to %q, want the resolved ip", stub.connected)
	}

	denied := &stubSocket{}
	socket = &FilteredTCPSocket{underlying: denied, policy: CommonPolicies.NoPrivateNetworks()}
	if err := socket.ConnectResolved(context.Background(), "api.example.com", "127.0.0.1", 443); err == nil {
		t.Fatal("expected denial")
	}
	if denied.connected != "" {
		t.Fatal("a denied connection reached the underlying socket")
	}
}
