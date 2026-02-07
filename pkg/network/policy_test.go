package network

import (
	"testing"
)

func TestAllowAllPolicy(t *testing.T) {
	policy := NewAllowAllPolicy()

	if err := policy.CheckConnection(ProtocolTCP, DirectionOutbound, "example.com", 80); err != nil {
		t.Errorf("AllowAllPolicy should allow TCP outbound: %v", err)
	}

	if err := policy.CheckConnection(ProtocolUDP, DirectionInbound, "localhost", 53); err != nil {
		t.Errorf("AllowAllPolicy should allow UDP inbound: %v", err)
	}

	if err := policy.CheckHTTP("GET", "api.example.com", 443, "/data"); err != nil {
		t.Errorf("AllowAllPolicy should allow HTTP: %v", err)
	}

	if policy.Description() == "" {
		t.Error("AllowAllPolicy should have a description")
	}
}

func TestDenyAllPolicy(t *testing.T) {
	policy := NewDenyAllPolicy()

	if err := policy.CheckConnection(ProtocolTCP, DirectionOutbound, "example.com", 80); err == nil {
		t.Error("DenyAllPolicy should deny TCP outbound")
	}

	if err := policy.CheckConnection(ProtocolUDP, DirectionInbound, "localhost", 53); err == nil {
		t.Error("DenyAllPolicy should deny UDP inbound")
	}

	if err := policy.CheckHTTP("GET", "api.example.com", 443, "/data"); err == nil {
		t.Error("DenyAllPolicy should deny HTTP")
	}

	if policy.Description() == "" {
		t.Error("DenyAllPolicy should have a description")
	}
}

func TestRuleBasedPolicy_AllowSpecificHost(t *testing.T) {
	policy := NewRuleBasedPolicy(ActionDeny)
	policy.AddRule(&NetworkRule{
		Action:    ActionAllow,
		Protocol:  ProtocolAny,
		Direction: DirectionAny,
		Hosts:     []string{"api.example.com"},
	})

	if err := policy.CheckConnection(ProtocolTCP, DirectionOutbound, "api.example.com", 443); err != nil {
		t.Errorf("Should allow api.example.com: %v", err)
	}

	if err := policy.CheckConnection(ProtocolTCP, DirectionOutbound, "evil.com", 80); err == nil {
		t.Error("Should deny evil.com")
	}
}

func TestRuleBasedPolicy_AllowSpecificPort(t *testing.T) {
	policy := NewRuleBasedPolicy(ActionDeny)
	policy.AddRule(&NetworkRule{
		Action:    ActionAllow,
		Protocol:  ProtocolAny,
		Direction: DirectionAny,
		Ports:     []string{"80", "443"},
	})

	if err := policy.CheckConnection(ProtocolTCP, DirectionOutbound, "example.com", 80); err != nil {
		t.Errorf("Should allow port 80: %v", err)
	}
	if err := policy.CheckConnection(ProtocolTCP, DirectionOutbound, "example.com", 443); err != nil {
		t.Errorf("Should allow port 443: %v", err)
	}

	if err := policy.CheckConnection(ProtocolTCP, DirectionOutbound, "example.com", 8080); err == nil {
		t.Error("Should deny port 8080")
	}
}

func TestRuleBasedPolicy_ProtocolFiltering(t *testing.T) {
	policy := NewRuleBasedPolicy(ActionDeny)
	policy.AddRule(&NetworkRule{
		Action:    ActionAllow,
		Protocol:  ProtocolTCP,
		Direction: DirectionAny,
	})

	if err := policy.CheckConnection(ProtocolTCP, DirectionOutbound, "example.com", 80); err != nil {
		t.Errorf("Should allow TCP: %v", err)
	}

	if err := policy.CheckConnection(ProtocolUDP, DirectionOutbound, "example.com", 53); err == nil {
		t.Error("Should deny UDP")
	}
}

func TestRuleBasedPolicy_DirectionFiltering(t *testing.T) {
	policy := NewRuleBasedPolicy(ActionDeny)
	policy.AddRule(&NetworkRule{
		Action:    ActionAllow,
		Protocol:  ProtocolAny,
		Direction: DirectionOutbound,
	})

	if err := policy.CheckConnection(ProtocolTCP, DirectionOutbound, "example.com", 80); err != nil {
		t.Errorf("Should allow outbound: %v", err)
	}

	if err := policy.CheckConnection(ProtocolTCP, DirectionInbound, "example.com", 80); err == nil {
		t.Error("Should deny inbound")
	}
}

func TestRuleBasedPolicy_DenyOverride(t *testing.T) {
	policy := NewRuleBasedPolicy(ActionAllow)
	policy.AddRule(&NetworkRule{
		Action:    ActionDeny,
		Protocol:  ProtocolAny,
		Direction: DirectionAny,
		Hosts:     []string{"blocked.com"},
	})

	if err := policy.CheckConnection(ProtocolTCP, DirectionOutbound, "blocked.com", 80); err == nil {
		t.Error("Should deny blocked.com")
	}

	if err := policy.CheckConnection(ProtocolTCP, DirectionOutbound, "allowed.com", 80); err != nil {
		t.Errorf("Should allow allowed.com: %v", err)
	}
}

func TestRuleBasedPolicy_CheckHTTP(t *testing.T) {
	policy := NewRuleBasedPolicy(ActionDeny)
	policy.AddRule(&NetworkRule{
		Action:    ActionAllow,
		Protocol:  ProtocolAny,
		Direction: DirectionAny,
		Hosts:     []string{"api.example.com"},
		Ports:     []string{"443"},
	})

	if err := policy.CheckHTTP("GET", "api.example.com", 443, "/users"); err != nil {
		t.Errorf("Should allow HTTPS to api.example.com: %v", err)
	}

	if err := policy.CheckHTTP("GET", "api.example.com", 80, "/users"); err == nil {
		t.Error("Should deny HTTP (port 80)")
	}

	if err := policy.CheckHTTP("GET", "other.com", 443, "/data"); err == nil {
		t.Error("Should deny other.com")
	}
}

func TestNetworkRule_Matches(t *testing.T) {
	tests := []struct {
		name      string
		rule      *NetworkRule
		protocol  Protocol
		direction Direction
		host      string
		port      int
		want      bool
	}{
		{
			name:      "wildcard rule matches all",
			rule:      &NetworkRule{Action: ActionAllow, Protocol: ProtocolAny, Direction: DirectionAny},
			protocol:  ProtocolTCP,
			direction: DirectionOutbound,
			host:      "example.com",
			port:      80,
			want:      true,
		},
		{
			name:      "protocol match",
			rule:      &NetworkRule{Action: ActionAllow, Protocol: ProtocolTCP, Direction: DirectionAny},
			protocol:  ProtocolTCP,
			direction: DirectionOutbound,
			host:      "example.com",
			port:      80,
			want:      true,
		},
		{
			name:      "protocol mismatch",
			rule:      &NetworkRule{Action: ActionAllow, Protocol: ProtocolTCP, Direction: DirectionAny},
			protocol:  ProtocolUDP,
			direction: DirectionOutbound,
			host:      "example.com",
			port:      80,
			want:      false,
		},
		{
			name:      "direction match",
			rule:      &NetworkRule{Action: ActionAllow, Protocol: ProtocolAny, Direction: DirectionOutbound},
			protocol:  ProtocolTCP,
			direction: DirectionOutbound,
			host:      "example.com",
			port:      80,
			want:      true,
		},
		{
			name:      "direction mismatch",
			rule:      &NetworkRule{Action: ActionAllow, Protocol: ProtocolAny, Direction: DirectionInbound},
			protocol:  ProtocolTCP,
			direction: DirectionOutbound,
			host:      "example.com",
			port:      80,
			want:      false,
		},
		{
			name:      "port match",
			rule:      &NetworkRule{Action: ActionAllow, Protocol: ProtocolAny, Direction: DirectionAny, Ports: []string{"80", "443"}},
			protocol:  ProtocolTCP,
			direction: DirectionOutbound,
			host:      "example.com",
			port:      80,
			want:      true,
		},
		{
			name:      "port mismatch",
			rule:      &NetworkRule{Action: ActionAllow, Protocol: ProtocolAny, Direction: DirectionAny, Ports: []string{"80", "443"}},
			protocol:  ProtocolTCP,
			direction: DirectionOutbound,
			host:      "example.com",
			port:      8080,
			want:      false,
		},
		{
			name:      "host match",
			rule:      &NetworkRule{Action: ActionAllow, Protocol: ProtocolAny, Direction: DirectionAny, Hosts: []string{"example.com"}},
			protocol:  ProtocolTCP,
			direction: DirectionOutbound,
			host:      "example.com",
			port:      80,
			want:      true,
		},
		{
			name:      "host mismatch",
			rule:      &NetworkRule{Action: ActionAllow, Protocol: ProtocolAny, Direction: DirectionAny, Hosts: []string{"example.com"}},
			protocol:  ProtocolTCP,
			direction: DirectionOutbound,
			host:      "other.com",
			port:      80,
			want:      false,
		},
		{
			name:      "complex match",
			rule:      &NetworkRule{Action: ActionAllow, Protocol: ProtocolTCP, Direction: DirectionOutbound, Hosts: []string{"api.example.com"}, Ports: []string{"443"}},
			protocol:  ProtocolTCP,
			direction: DirectionOutbound,
			host:      "api.example.com",
			port:      443,
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.rule.Matches(tt.protocol, tt.direction, tt.host, tt.port)
			if got != tt.want {
				t.Errorf("Matches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHostListPolicy(t *testing.T) {
	allowHosts := []string{"api.example.com", "cdn.example.com"}
	denyHosts := []string{"blocked.com"}

	policy := NewHostListPolicy(allowHosts, denyHosts)

	if err := policy.CheckConnection(ProtocolTCP, DirectionOutbound, "api.example.com", 443); err != nil {
		t.Errorf("Should allow api.example.com: %v", err)
	}

	if err := policy.CheckConnection(ProtocolTCP, DirectionOutbound, "blocked.com", 80); err == nil {
		t.Error("Should deny blocked.com")
	}

	if err := policy.CheckConnection(ProtocolTCP, DirectionOutbound, "unknown.com", 80); err == nil {
		t.Error("Should deny unknown.com when allow list is specified")
	}
}

func TestHostListPolicy_EmptyAllowList(t *testing.T) {
	denyHosts := []string{"blocked.com"}
	policy := NewHostListPolicy(nil, denyHosts)

	if err := policy.CheckConnection(ProtocolTCP, DirectionOutbound, "example.com", 80); err != nil {
		t.Errorf("Should allow example.com: %v", err)
	}

	if err := policy.CheckConnection(ProtocolTCP, DirectionOutbound, "blocked.com", 80); err == nil {
		t.Error("Should deny blocked.com")
	}
}

func TestParseRule(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"allow:tcp:outbound:80,443:*", false},
		{"deny:udp:inbound:53:*", false},
		{"allow:*:*:*:example.com", false},
		{"invalid", true},
		{"invalid:action:out:80", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, err := ParseRule(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseRule() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCommonPolicies(t *testing.T) {
	// Test WebOnly
	webOnly := CommonPolicies.WebOnly()
	if err := webOnly.CheckConnection(ProtocolTCP, DirectionOutbound, "example.com", 80); err != nil {
		t.Errorf("WebOnly should allow port 80: %v", err)
	}
	if err := webOnly.CheckConnection(ProtocolTCP, DirectionOutbound, "example.com", 443); err != nil {
		t.Errorf("WebOnly should allow port 443: %v", err)
	}

	// Test DNSOnly
	dnsOnly := CommonPolicies.DNSOnly()
	if err := dnsOnly.CheckConnection(ProtocolUDP, DirectionOutbound, "8.8.8.8", 53); err != nil {
		t.Errorf("DNSOnly should allow UDP 53: %v", err)
	}

	// Test LocalhostOnly
	localhostOnly := CommonPolicies.LocalhostOnly()
	if err := localhostOnly.CheckConnection(ProtocolTCP, DirectionOutbound, "localhost", 8080); err != nil {
		t.Errorf("LocalhostOnly should allow localhost: %v", err)
	}
	if err := localhostOnly.CheckConnection(ProtocolTCP, DirectionOutbound, "127.0.0.1", 8080); err != nil {
		t.Errorf("LocalhostOnly should allow 127.0.0.1: %v", err)
	}
}
