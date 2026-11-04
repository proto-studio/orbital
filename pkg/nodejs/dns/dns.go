// Package dns implements the Node.js dns module on top of the runtime's injected resolver.
package dns

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"

	"proto.zip/studio/orbital/pkg/nodejs/internal/netdial"
	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8"
)

//go:embed dns.js
var dnsJS string

// DNS provides DNS lookup functionality.
type DNS struct {
	rt *runtime.Runtime
}

// New creates a new DNS module.
func New() *DNS {
	return &DNS{}
}

// Name returns the module name.
func (d *DNS) Name() string {
	return "dns"
}

// Register sets up the dns module.
func (d *DNS) Register(rt *runtime.Runtime) error {
	d.rt = rt
	iso := rt.Isolate()
	ctx := rt.Context()

	internal, err := ctx.NewObject()
	if err != nil {
		return err
	}
	funcs := map[string]v8.FunctionCallback{
		"lookup":        d.lookupFunc,
		"query":         d.queryFunc,
		"reverse":       d.reverseFunc,
		"lookupService": d.lookupServiceFunc,
		"systemServers": d.systemServersFunc,
	}
	for name, fn := range funcs {
		tmpl, err := iso.NewFunctionTemplate(fn)
		if err != nil {
			return err
		}
		val, err := tmpl.GetFunction(ctx)
		if err != nil {
			return err
		}
		if err := internal.Set(name, val); err != nil {
			return err
		}
	}
	if err := rt.SetGlobal("__dns_internal", internal); err != nil {
		return err
	}
	_, err = rt.RunScript(dnsJS, "dns.js")
	return err
}

func jsString(ctx *v8.Context, s string) *v8.Value {
	v, err := ctx.NewString(s)
	if err != nil {
		return ctx.Null()
	}
	return v
}

// finish delivers a result as (jsonError, jsonResult).
func (d *DNS) finish(callback *v8.Value, code string, result any) {
	d.rt.EventLoop().EnqueueMicrotask(func() {
		ctx := d.rt.Context()
		errVal := ctx.Null()
		resultVal := ctx.Null()
		if code != "" {
			errVal = jsString(ctx, code)
		} else if encoded, err := json.Marshal(result); err == nil {
			resultVal = jsString(ctx, string(encoded))
		}
		d.rt.MakeCallback(callback, ctx.Undefined(), errVal, resultVal)
		ctx.PerformMicrotaskCheckpoint()
	})
}

func (d *DNS) async(work func()) {
	d.rt.EventLoop().AddPendingWork()
	go func() {
		defer d.rt.EventLoop().DonePendingWork()
		work()
	}()
}

func (d *DNS) context() context.Context {
	if ctx := d.rt.ExecutionContext(); ctx != nil {
		return ctx
	}
	return context.Background()
}

func errorCode(err error) string {
	var queryErr *runtime.DNSQueryError
	if asQuery(err, &queryErr) {
		return queryErr.Code
	}
	if err == context.Canceled {
		return "ECANCELLED"
	}
	desc := netdial.Describe(err, "getaddrinfo", "", 0)
	if desc.Code != "" {
		return desc.Code
	}
	return "ENOTFOUND"
}

func asQuery(err error, target **runtime.DNSQueryError) bool {
	if q, ok := err.(*runtime.DNSQueryError); ok {
		*target = q
		return true
	}
	return false
}

// lookup(host, family, callback) resolves a name as getaddrinfo would. callback receives
// (code, jsonAddresses) with entries of {address, family}.
func (d *DNS) lookupFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 3 {
		return nil
	}
	host := args[0].String()
	family := int(args[1].Integer())
	callback := args[2]

	d.async(func() {
		network := "ip"
		switch family {
		case 4:
			network = "ip4"
		case 6:
			network = "ip6"
		}
		ips, err := d.rt.DNSResolver().LookupIP(d.context(), network, host)
		if err != nil {
			d.finish(callback, errorCode(err), nil)
			return
		}
		type address struct {
			Address string `json:"address"`
			Family  int    `json:"family"`
		}
		out := make([]address, 0, len(ips))
		for _, ip := range ips {
			if v4 := ip.To4(); v4 != nil {
				if family != 6 {
					out = append(out, address{v4.String(), 4})
				}
			} else if family != 4 {
				out = append(out, address{ip.String(), 6})
			}
		}
		if len(out) == 0 {
			d.finish(callback, "ENOTFOUND", nil)
			return
		}
		d.finish(callback, "", out)
	})
	return nil
}

// query(rrtype, name, jsonServers, callback) asks for records. callback receives (code,
// jsonRecords). Script-chosen servers are honored only with both the real resolver and
// real sockets: elsewhere a query to a server of the script's choosing would be traffic
// the embedder's controls never saw.
func (d *DNS) queryFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 4 {
		return nil
	}
	rrtype := args[0].String()
	name := args[1].String()
	var servers []string
	if err := json.Unmarshal([]byte(args[2].String()), &servers); err != nil {
		return info.Context().Throw("dns: invalid servers")
	}
	callback := args[3]

	d.async(func() {
		resolver := d.rt.DNSResolver()
		if records := runtime.RecordsFor(resolver); records != nil {
			if _, real := d.rt.SocketFactory().(*runtime.RealSocketFactory); !real {
				servers = nil
			}
			result, err := records.LookupRecords(d.context(), name, rrtype, servers)
			if err != nil {
				d.finish(callback, errorCode(err), nil)
				return
			}
			d.finish(callback, "", result)
			return
		}
		result, err := d.queryResolver(resolver, name, rrtype)
		if err != nil {
			d.finish(callback, errorCode(err), nil)
			return
		}
		d.finish(callback, "", result)
	})
	return nil
}

// queryResolver answers from a Resolver without direct record access.
func (d *DNS) queryResolver(resolver runtime.Resolver, name, rrtype string) ([]map[string]any, error) {
	ctx := d.context()
	var out []map[string]any
	switch rrtype {
	case "A", "AAAA":
		network := "ip4"
		if rrtype == "AAAA" {
			network = "ip6"
		}
		ips, err := resolver.LookupIP(ctx, network, name)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if (ip.To4() != nil) == (rrtype == "A") {
				out = append(out, map[string]any{"type": rrtype, "address": ip.String(), "ttl": 0})
			}
		}
	case "CNAME":
		cname, err := resolver.LookupCNAME(ctx, name)
		if err != nil {
			return nil, err
		}
		cname = strings.TrimSuffix(cname, ".")
		if strings.EqualFold(cname, strings.TrimSuffix(name, ".")) {
			return nil, &runtime.DNSQueryError{Code: "ENODATA"}
		}
		out = append(out, map[string]any{"type": "CNAME", "value": cname})
	case "MX":
		records, err := resolver.LookupMX(ctx, name)
		if err != nil {
			return nil, err
		}
		for _, mx := range records {
			out = append(out, map[string]any{"type": "MX", "exchange": strings.TrimSuffix(mx.Host, "."), "priority": int(mx.Pref)})
		}
	case "NS":
		records, err := resolver.LookupNS(ctx, name)
		if err != nil {
			return nil, err
		}
		for _, ns := range records {
			out = append(out, map[string]any{"type": "NS", "value": strings.TrimSuffix(ns.Host, ".")})
		}
	case "TXT":
		records, err := resolver.LookupTXT(ctx, name)
		if err != nil {
			return nil, err
		}
		for _, txt := range records {
			out = append(out, map[string]any{"type": "TXT", "entries": []string{txt}})
		}
	case "SRV":
		_, records, err := resolver.LookupSRV(ctx, "", "", name)
		if err != nil {
			return nil, err
		}
		for _, srv := range records {
			out = append(out, map[string]any{"type": "SRV", "name": strings.TrimSuffix(srv.Target, "."),
				"port": int(srv.Port), "priority": int(srv.Priority), "weight": int(srv.Weight)})
		}
	default:
		return nil, &runtime.DNSQueryError{Code: "ENOTIMP"}
	}
	if len(out) == 0 {
		return nil, &runtime.DNSQueryError{Code: "ENODATA"}
	}
	return out, nil
}

// reverse(ip, jsonServers, callback) returns the names of an address. callback receives
// (code, jsonNames).
func (d *DNS) reverseFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 3 {
		return nil
	}
	ip := args[0].String()
	var servers []string
	if err := json.Unmarshal([]byte(args[1].String()), &servers); err != nil {
		return info.Context().Throw("dns: invalid servers")
	}
	callback := args[2]
	d.async(func() {
		resolver := d.rt.DNSResolver()
		if records := runtime.RecordsFor(resolver); records != nil {
			if _, real := d.rt.SocketFactory().(*runtime.RealSocketFactory); !real {
				servers = nil
			}
			name := reverseName(ip)
			if name == "" {
				d.finish(callback, "EINVAL", nil)
				return
			}
			result, err := records.LookupRecords(d.context(), name, "PTR", servers)
			if err != nil {
				d.finish(callback, errorCode(err), nil)
				return
			}
			names := make([]string, 0, len(result))
			for _, record := range result {
				if value, ok := record["value"].(string); ok {
					names = append(names, value)
				}
			}
			d.finish(callback, "", names)
			return
		}
		names, err := resolver.LookupAddr(d.context(), ip)
		if err != nil {
			d.finish(callback, errorCode(err), nil)
			return
		}
		for i, name := range names {
			names[i] = strings.TrimSuffix(name, ".")
		}
		sort.Strings(names)
		d.finish(callback, "", names)
	})
	return nil
}

// reverseName returns the name PTR records for ip live under.
func reverseName(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ""
	}
	if v4 := parsed.To4(); v4 != nil {
		return fmt.Sprintf("%d.%d.%d.%d.in-addr.arpa", v4[3], v4[2], v4[1], v4[0])
	}
	var name strings.Builder
	for i := len(parsed) - 1; i >= 0; i-- {
		fmt.Fprintf(&name, "%x.%x.", parsed[i]&0xf, parsed[i]>>4)
	}
	return name.String() + "ip6.arpa"
}

// lookupService(ip, port, callback) gives a host and service name for an address.
func (d *DNS) lookupServiceFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	args := info.Args()
	if len(args) < 3 {
		return nil
	}
	ip := args[0].String()
	port := int(args[1].Integer())
	callback := args[2]
	d.async(func() {
		host := ip
		if names, err := d.rt.DNSResolver().LookupAddr(d.context(), ip); err == nil && len(names) > 0 {
			host = strings.TrimSuffix(names[0], ".")
		}
		d.finish(callback, "", map[string]string{"hostname": host, "service": runtime.LookupServiceName(port, "tcp")})
	})
	return nil
}

// systemServers() returns the DNS servers in use, as JSON.
func (d *DNS) systemServersFunc(info *v8.FunctionCallbackInfo) *v8.Value {
	ctx := info.Context()
	servers := []string{}
	if _, real := d.rt.DNSResolver().(*runtime.RealResolver); real {
		for _, server := range runtime.SystemDNSServers() {
			host, _, err := net.SplitHostPort(server)
			if err == nil {
				servers = append(servers, host)
			}
		}
	}
	encoded, _ := json.Marshal(servers)
	return jsString(ctx, string(encoded))
}
