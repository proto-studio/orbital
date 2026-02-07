// Package network - Network policy and firewall rules.
package network

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// Protocol represents a network protocol.
type Protocol string

const (
	// ProtocolTCP represents TCP protocol.
	ProtocolTCP Protocol = "tcp"
	// ProtocolUDP represents UDP protocol.
	ProtocolUDP Protocol = "udp"
	// ProtocolAny matches any protocol.
	ProtocolAny Protocol = "*"
)

// Direction represents traffic direction.
type Direction string

const (
	// DirectionInbound represents incoming traffic (listen/bind).
	DirectionInbound Direction = "inbound"
	// DirectionOutbound represents outgoing traffic (connect/send).
	DirectionOutbound Direction = "outbound"
	// DirectionAny matches any direction.
	DirectionAny Direction = "*"
)

// Action represents what to do when a rule matches.
type Action string

const (
	// ActionAllow permits the connection.
	ActionAllow Action = "allow"
	// ActionDeny blocks the connection.
	ActionDeny Action = "deny"
)

var (
	// ErrConnectionDenied is returned when a connection is denied by policy.
	ErrConnectionDenied = errors.New("EACCES: connection denied by network policy")
	// ErrPortDenied is returned when a port is not allowed.
	ErrPortDenied = errors.New("EACCES: port not allowed by network policy")
	// ErrProtocolDenied is returned when a protocol is not allowed.
	ErrProtocolDenied = errors.New("EACCES: protocol not allowed by network policy")
)

// NetworkRule defines a single network access rule.
type NetworkRule struct {
	// Action is what to do when this rule matches (allow/deny).
	Action Action
	// Protocol is the protocol to match (tcp/udp/*).
	Protocol Protocol
	// Direction is the traffic direction (inbound/outbound/*).
	Direction Direction
	// Ports is a list of allowed ports or port ranges (e.g., "80", "8000-9000", "*").
	Ports []string
	// Hosts is a list of allowed hosts (supports wildcards like "*.example.com").
	Hosts []string
	// Description is a human-readable description of the rule.
	Description string
}

// Matches checks if this rule matches the given connection parameters.
func (r *NetworkRule) Matches(protocol Protocol, direction Direction, host string, port int) bool {
	// Check protocol
	if r.Protocol != ProtocolAny && r.Protocol != protocol {
		return false
	}

	// Check direction
	if r.Direction != DirectionAny && r.Direction != direction {
		return false
	}

	// Check ports
	if !r.matchesPort(port) {
		return false
	}

	// Check hosts
	if !r.matchesHost(host) {
		return false
	}

	return true
}

// matchesPort checks if the port matches any of the rule's port specifications.
func (r *NetworkRule) matchesPort(port int) bool {
	// If no ports specified, match all
	if len(r.Ports) == 0 {
		return true
	}

	for _, p := range r.Ports {
		if p == "*" {
			return true
		}

		// Check for range (e.g., "8000-9000")
		if strings.Contains(p, "-") {
			parts := strings.SplitN(p, "-", 2)
			if len(parts) == 2 {
				low, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
				high, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
				if err1 == nil && err2 == nil && port >= low && port <= high {
					return true
				}
			}
			continue
		}

		// Single port
		if portNum, err := strconv.Atoi(p); err == nil && portNum == port {
			return true
		}
	}

	return false
}

// matchesHost checks if the host matches any of the rule's host patterns.
func (r *NetworkRule) matchesHost(host string) bool {
	// If no hosts specified, match all
	if len(r.Hosts) == 0 {
		return true
	}

	host = strings.ToLower(host)

	for _, pattern := range r.Hosts {
		if pattern == "*" {
			return true
		}

		pattern = strings.ToLower(pattern)

		// Exact match
		if pattern == host {
			return true
		}

		// Wildcard pattern
		if strings.Contains(pattern, "*") {
			regexPattern := "^" + regexp.QuoteMeta(pattern) + "$"
			regexPattern = strings.ReplaceAll(regexPattern, `\*`, `.*`)
			if re, err := regexp.Compile(regexPattern); err == nil && re.MatchString(host) {
				return true
			}
		}

		// CIDR notation for IP addresses
		if strings.Contains(pattern, "/") {
			_, ipNet, err := net.ParseCIDR(pattern)
			if err == nil {
				ip := net.ParseIP(host)
				if ip != nil && ipNet.Contains(ip) {
					return true
				}
			}
		}
	}

	return false
}

// NetworkPolicy defines the interface for network access control.
type NetworkPolicy interface {
	// CheckConnection checks if a connection is allowed.
	// Returns nil if allowed, error if denied.
	CheckConnection(protocol Protocol, direction Direction, host string, port int) error

	// CheckHTTP checks if an HTTP request is allowed.
	// Returns nil if allowed, error if denied.
	CheckHTTP(method string, host string, port int, path string) error

	// Description returns a human-readable description of the policy.
	Description() string
}

// AllowAllPolicy permits all network operations.
type AllowAllPolicy struct{}

// NewAllowAllPolicy creates a policy that allows all connections.
func NewAllowAllPolicy() *AllowAllPolicy {
	return &AllowAllPolicy{}
}

// CheckConnection always returns nil (allowed).
func (p *AllowAllPolicy) CheckConnection(protocol Protocol, direction Direction, host string, port int) error {
	return nil
}

// CheckHTTP always returns nil (allowed).
func (p *AllowAllPolicy) CheckHTTP(method string, host string, port int, path string) error {
	return nil
}

// Description returns a description of this policy.
func (p *AllowAllPolicy) Description() string {
	return "allow all network connections"
}

// DenyAllPolicy blocks all network operations.
type DenyAllPolicy struct{}

// NewDenyAllPolicy creates a policy that denies all connections.
func NewDenyAllPolicy() *DenyAllPolicy {
	return &DenyAllPolicy{}
}

// CheckConnection always returns ErrConnectionDenied.
func (p *DenyAllPolicy) CheckConnection(protocol Protocol, direction Direction, host string, port int) error {
	return ErrConnectionDenied
}

// CheckHTTP always returns ErrConnectionDenied.
func (p *DenyAllPolicy) CheckHTTP(method string, host string, port int, path string) error {
	return ErrConnectionDenied
}

// Description returns a description of this policy.
func (p *DenyAllPolicy) Description() string {
	return "deny all network connections"
}

// RuleBasedPolicy implements NetworkPolicy using a list of rules.
// Rules are evaluated in order; the first matching rule determines the action.
// If no rules match, the default action is used.
type RuleBasedPolicy struct {
	// Rules is the list of rules to evaluate in order.
	Rules []*NetworkRule
	// DefaultAction is the action when no rules match (default: deny).
	DefaultAction Action
}

// NewRuleBasedPolicy creates a new rule-based policy.
// If defaultAction is not provided, it defaults to ActionDeny.
func NewRuleBasedPolicy(defaultAction ...Action) *RuleBasedPolicy {
	action := ActionDeny
	if len(defaultAction) > 0 {
		action = defaultAction[0]
	}
	return &RuleBasedPolicy{
		Rules:         make([]*NetworkRule, 0),
		DefaultAction: action,
	}
}

// HostListPolicy is a simple policy based on allow/deny host lists.
// Deny list takes precedence over allow list.
type HostListPolicy struct {
	allowHosts []string
	denyHosts  []string
}

// NewHostListPolicy creates a policy based on host lists.
// If allowHosts is empty, all hosts are allowed except those in denyHosts.
// If allowHosts is non-empty, only those hosts are allowed (minus denyHosts).
func NewHostListPolicy(allowHosts, denyHosts []string) *HostListPolicy {
	return &HostListPolicy{
		allowHosts: allowHosts,
		denyHosts:  denyHosts,
	}
}

// CheckConnection checks if a connection is allowed.
func (p *HostListPolicy) CheckConnection(protocol Protocol, direction Direction, host string, port int) error {
	host = strings.ToLower(host)

	// Check deny list first (takes precedence)
	for _, denied := range p.denyHosts {
		if matchHostPattern(host, strings.ToLower(denied)) {
			return fmt.Errorf("%w: host %s is denied", ErrConnectionDenied, host)
		}
	}

	// If no allow list, allow everything not denied
	if len(p.allowHosts) == 0 {
		return nil
	}

	// Check allow list
	for _, allowed := range p.allowHosts {
		if matchHostPattern(host, strings.ToLower(allowed)) {
			return nil
		}
	}

	return fmt.Errorf("%w: host %s is not in allow list", ErrConnectionDenied, host)
}

// CheckHTTP checks if an HTTP request is allowed.
func (p *HostListPolicy) CheckHTTP(method string, host string, port int, path string) error {
	return p.CheckConnection(ProtocolTCP, DirectionOutbound, host, port)
}

// Description returns a description of this policy.
func (p *HostListPolicy) Description() string {
	return fmt.Sprintf("host list policy (allow: %d, deny: %d)", len(p.allowHosts), len(p.denyHosts))
}

// matchHostPattern checks if a host matches a pattern.
func matchHostPattern(host, pattern string) bool {
	if pattern == host {
		return true
	}
	if pattern == "*" {
		return true
	}
	// Simple wildcard matching
	if strings.HasPrefix(pattern, "*.") {
		suffix := pattern[1:] // ".example.com"
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// AddRule adds a rule to the policy.
func (p *RuleBasedPolicy) AddRule(rule *NetworkRule) {
	p.Rules = append(p.Rules, rule)
}

// AllowTCP adds a rule allowing TCP on specified ports.
func (p *RuleBasedPolicy) AllowTCP(direction Direction, ports ...string) {
	p.AddRule(&NetworkRule{
		Action:    ActionAllow,
		Protocol:  ProtocolTCP,
		Direction: direction,
		Ports:     ports,
		Hosts:     []string{"*"},
	})
}

// AllowUDP adds a rule allowing UDP on specified ports.
func (p *RuleBasedPolicy) AllowUDP(direction Direction, ports ...string) {
	p.AddRule(&NetworkRule{
		Action:    ActionAllow,
		Protocol:  ProtocolUDP,
		Direction: direction,
		Ports:     ports,
		Hosts:     []string{"*"},
	})
}

// AllowHost adds a rule allowing connections to specific hosts.
func (p *RuleBasedPolicy) AllowHost(protocol Protocol, direction Direction, hosts ...string) {
	p.AddRule(&NetworkRule{
		Action:    ActionAllow,
		Protocol:  protocol,
		Direction: direction,
		Ports:     []string{"*"},
		Hosts:     hosts,
	})
}

// DenyHost adds a rule denying connections to specific hosts.
func (p *RuleBasedPolicy) DenyHost(protocol Protocol, direction Direction, hosts ...string) {
	p.AddRule(&NetworkRule{
		Action:    ActionDeny,
		Protocol:  protocol,
		Direction: direction,
		Ports:     []string{"*"},
		Hosts:     hosts,
	})
}

// CheckConnection checks if a connection is allowed by the rules.
func (p *RuleBasedPolicy) CheckConnection(protocol Protocol, direction Direction, host string, port int) error {
	for _, rule := range p.Rules {
		if rule.Matches(protocol, direction, host, port) {
			if rule.Action == ActionAllow {
				return nil
			}
			return fmt.Errorf("%w: %s %s to %s:%d denied by rule: %s",
				ErrConnectionDenied, protocol, direction, host, port, rule.Description)
		}
	}

	// No matching rule - use default action
	if p.DefaultAction == ActionAllow {
		return nil
	}
	return fmt.Errorf("%w: %s %s to %s:%d denied by default policy",
		ErrConnectionDenied, protocol, direction, host, port)
}

// CheckHTTP checks if an HTTP request is allowed.
func (p *RuleBasedPolicy) CheckHTTP(method string, host string, port int, path string) error {
	// HTTP uses TCP
	return p.CheckConnection(ProtocolTCP, DirectionOutbound, host, port)
}

// Description returns a summary of this policy.
func (p *RuleBasedPolicy) Description() string {
	if len(p.Rules) == 0 {
		return fmt.Sprintf("rule-based policy (0 rules, default: %s)", p.DefaultAction)
	}
	return fmt.Sprintf("rule-based policy (%d rules, default: %s)", len(p.Rules), p.DefaultAction)
}

// ParseRule parses a rule string into a NetworkRule.
// Format: "action:protocol:direction:ports:hosts"
// Examples:
//   - "allow:tcp:outbound:80,443:*" - Allow TCP outbound to ports 80,443
//   - "allow:udp:outbound:53:*" - Allow UDP outbound DNS
//   - "deny:*:*:*:10.0.0.0/8" - Deny all to private IPs
//   - "allow:tcp:inbound:8080:*" - Allow TCP inbound on 8080
func ParseRule(s string) (*NetworkRule, error) {
	parts := strings.Split(s, ":")
	if len(parts) < 4 {
		return nil, fmt.Errorf("invalid rule format: expected at least 4 colon-separated parts")
	}

	rule := &NetworkRule{}

	// Action
	switch strings.ToLower(parts[0]) {
	case "allow":
		rule.Action = ActionAllow
	case "deny":
		rule.Action = ActionDeny
	default:
		return nil, fmt.Errorf("invalid action: %s (expected allow/deny)", parts[0])
	}

	// Protocol
	switch strings.ToLower(parts[1]) {
	case "tcp":
		rule.Protocol = ProtocolTCP
	case "udp":
		rule.Protocol = ProtocolUDP
	case "*", "any":
		rule.Protocol = ProtocolAny
	default:
		return nil, fmt.Errorf("invalid protocol: %s (expected tcp/udp/*)", parts[1])
	}

	// Direction
	switch strings.ToLower(parts[2]) {
	case "in", "inbound":
		rule.Direction = DirectionInbound
	case "out", "outbound":
		rule.Direction = DirectionOutbound
	case "*", "any":
		rule.Direction = DirectionAny
	default:
		return nil, fmt.Errorf("invalid direction: %s (expected in/out/*)", parts[2])
	}

	// Ports
	if parts[3] != "" && parts[3] != "*" {
		rule.Ports = strings.Split(parts[3], ",")
	}

	// Hosts (optional, defaults to *)
	if len(parts) > 4 && parts[4] != "" {
		rule.Hosts = strings.Split(parts[4], ",")
	}

	rule.Description = s
	return rule, nil
}

// ParseRules parses multiple rule strings.
func ParseRules(rules []string) ([]*NetworkRule, error) {
	result := make([]*NetworkRule, 0, len(rules))
	for _, s := range rules {
		rule, err := ParseRule(s)
		if err != nil {
			return nil, fmt.Errorf("error parsing rule %q: %w", s, err)
		}
		result = append(result, rule)
	}
	return result, nil
}

// CommonPolicies provides pre-built policies for common use cases.
var CommonPolicies = struct {
	// WebOnly allows only HTTP/HTTPS outbound.
	WebOnly func() *RuleBasedPolicy
	// DNSOnly allows only DNS queries.
	DNSOnly func() *RuleBasedPolicy
	// LocalhostOnly allows only localhost connections.
	LocalhostOnly func() *RuleBasedPolicy
	// NoPrivateNetworks blocks RFC1918 private networks.
	NoPrivateNetworks func() *RuleBasedPolicy
}{
	WebOnly: func() *RuleBasedPolicy {
		p := NewRuleBasedPolicy()
		p.AllowTCP(DirectionOutbound, "80", "443", "8080", "8443")
		return p
	},
	DNSOnly: func() *RuleBasedPolicy {
		p := NewRuleBasedPolicy()
		p.AllowUDP(DirectionOutbound, "53")
		p.AllowTCP(DirectionOutbound, "53")
		return p
	},
	LocalhostOnly: func() *RuleBasedPolicy {
		p := NewRuleBasedPolicy()
		p.AllowHost(ProtocolAny, DirectionAny, "localhost", "127.0.0.1", "::1")
		return p
	},
	NoPrivateNetworks: func() *RuleBasedPolicy {
		p := NewRuleBasedPolicy()
		p.DefaultAction = ActionAllow
		// Deny private networks first (rules evaluated in order)
		p.DenyHost(ProtocolAny, DirectionAny, "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8", "169.254.0.0/16")
		return p
	},
}
