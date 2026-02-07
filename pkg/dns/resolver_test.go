package dns

import (
	"context"
	"testing"
)

func TestRealResolver_LookupHost(t *testing.T) {
	resolver := NewRealResolver()
	ctx := context.Background()

	// Test lookup of localhost (should always resolve)
	addrs, err := resolver.LookupHost(ctx, "localhost")
	if err != nil {
		t.Errorf("LookupHost(localhost) failed: %v", err)
	}
	if len(addrs) == 0 {
		t.Error("LookupHost(localhost) returned no addresses")
	}
}

func TestRealResolver_LookupIP(t *testing.T) {
	resolver := NewRealResolver()
	ctx := context.Background()

	ips, err := resolver.LookupIP(ctx, "ip", "localhost")
	if err != nil {
		t.Errorf("LookupIP(localhost) failed: %v", err)
	}
	if len(ips) == 0 {
		t.Error("LookupIP(localhost) returned no IPs")
	}
}

func TestNoOpResolver(t *testing.T) {
	noop := NewNoOpResolver()
	ctx := context.Background()

	// All operations should fail
	_, err := noop.LookupHost(ctx, "localhost")
	if err == nil {
		t.Error("NoOp LookupHost should return error")
	}

	_, err = noop.LookupIP(ctx, "ip", "localhost")
	if err == nil {
		t.Error("NoOp LookupIP should return error")
	}

	_, err = noop.LookupCNAME(ctx, "localhost")
	if err == nil {
		t.Error("NoOp LookupCNAME should return error")
	}

	_, err = noop.LookupMX(ctx, "localhost")
	if err == nil {
		t.Error("NoOp LookupMX should return error")
	}

	_, err = noop.LookupTXT(ctx, "localhost")
	if err == nil {
		t.Error("NoOp LookupTXT should return error")
	}

	_, err = noop.LookupNS(ctx, "localhost")
	if err == nil {
		t.Error("NoOp LookupNS should return error")
	}

	_, _, err = noop.LookupSRV(ctx, "service", "proto", "name")
	if err == nil {
		t.Error("NoOp LookupSRV should return error")
	}

	_, err = noop.LookupAddr(ctx, "127.0.0.1")
	if err == nil {
		t.Error("NoOp LookupAddr should return error")
	}
}
