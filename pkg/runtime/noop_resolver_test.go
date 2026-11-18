package runtime

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"
)

// NoOpResolver answers as a host without network access does: localhost from the hosts
// file, nothing else.
func TestNoOpResolverAnswersAsWithoutNetwork(t *testing.T) {
	ctx := context.Background()
	r := NewNoOpResolver()

	if got, err := r.LookupHost(ctx, "localhost"); err != nil || !reflect.DeepEqual(got, []string{"127.0.0.1", "::1"}) {
		t.Errorf("LookupHost(localhost) = %v, %v", got, err)
	}
	if got, err := r.LookupIP(ctx, "ip6", "localhost"); err != nil || len(got) != 1 || !got[0].Equal(net.IPv6loopback) {
		t.Errorf("LookupIP(ip6, localhost) = %v, %v", got, err)
	}
	var dnsErr *net.DNSError
	if _, err := r.LookupIP(ctx, "ip", "example.com"); !errors.As(err, &dnsErr) || !dnsErr.IsNotFound {
		t.Errorf("LookupIP(example.com) error = %v, want not found", err)
	}

	records := RecordsFor(r)
	if records == nil {
		t.Fatal("RecordsFor(NoOpResolver) = nil")
	}
	var queryErr *DNSQueryError
	if _, err := records.LookupRecords(ctx, "localhost", "A", nil); !errors.As(err, &queryErr) || queryErr.Code != "ECONNREFUSED" {
		t.Errorf("A localhost error = %v, want ECONNREFUSED", err)
	}
	for _, name := range []string{"1.0.0.127.in-addr.arpa", "1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.ip6.arpa"} {
		got, err := records.LookupRecords(ctx, name, "PTR", nil)
		if err != nil || len(got) != 1 || got[0]["value"] != "localhost" {
			t.Errorf("PTR %s = %v, %v", name, got, err)
		}
	}
	if _, err := records.LookupRecords(ctx, "1.2.0.192.in-addr.arpa", "PTR", nil); !errors.As(err, &queryErr) || queryErr.Code != "ENOTFOUND" {
		t.Errorf("PTR 192.0.2.1 error = %v, want ENOTFOUND", err)
	}

	// A resolver that embeds NoOpResolver keeps its own lookups.
	type embedding struct{ NoOpResolver }
	if RecordsFor(&embedding{}) != nil {
		t.Error("an embedding type inherited NoOpResolver's record queries")
	}
}
