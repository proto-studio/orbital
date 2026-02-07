// Package dns implements the Node.js dns module.
package dns

import (
	"context"
	_ "embed"
	"strings"
	"time"

	"proto.zip/studio/orbital/pkg/runtime"
	"proto.zip/studio/orbital/pkg/v8go"
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

	// Create dns internal object for Go functions
	dnsInternal, err := ctx.NewObject()
	if err != nil {
		return err
	}

	// lookup
	lookupFn, err := iso.NewFunctionTemplate(d.lookupFunc)
	if err != nil {
		return err
	}
	lookupVal, err := lookupFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := dnsInternal.Set("lookup", lookupVal); err != nil {
		return err
	}

	// resolve
	resolveFn, err := iso.NewFunctionTemplate(d.resolveFunc)
	if err != nil {
		return err
	}
	resolveVal, err := resolveFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := dnsInternal.Set("resolve", resolveVal); err != nil {
		return err
	}

	// resolve4
	resolve4Fn, err := iso.NewFunctionTemplate(d.resolve4Func)
	if err != nil {
		return err
	}
	resolve4Val, err := resolve4Fn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := dnsInternal.Set("resolve4", resolve4Val); err != nil {
		return err
	}

	// resolve6
	resolve6Fn, err := iso.NewFunctionTemplate(d.resolve6Func)
	if err != nil {
		return err
	}
	resolve6Val, err := resolve6Fn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := dnsInternal.Set("resolve6", resolve6Val); err != nil {
		return err
	}

	// resolveMx
	resolveMxFn, err := iso.NewFunctionTemplate(d.resolveMxFunc)
	if err != nil {
		return err
	}
	resolveMxVal, err := resolveMxFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := dnsInternal.Set("resolveMx", resolveMxVal); err != nil {
		return err
	}

	// resolveTxt
	resolveTxtFn, err := iso.NewFunctionTemplate(d.resolveTxtFunc)
	if err != nil {
		return err
	}
	resolveTxtVal, err := resolveTxtFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := dnsInternal.Set("resolveTxt", resolveTxtVal); err != nil {
		return err
	}

	// resolveNs
	resolveNsFn, err := iso.NewFunctionTemplate(d.resolveNsFunc)
	if err != nil {
		return err
	}
	resolveNsVal, err := resolveNsFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := dnsInternal.Set("resolveNs", resolveNsVal); err != nil {
		return err
	}

	// resolveCname
	resolveCnameFn, err := iso.NewFunctionTemplate(d.resolveCnameFunc)
	if err != nil {
		return err
	}
	resolveCnameVal, err := resolveCnameFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := dnsInternal.Set("resolveCname", resolveCnameVal); err != nil {
		return err
	}

	// reverse
	reverseFn, err := iso.NewFunctionTemplate(d.reverseFunc)
	if err != nil {
		return err
	}
	reverseVal, err := reverseFn.GetFunction(ctx)
	if err != nil {
		return err
	}
	if err := dnsInternal.Set("reverse", reverseVal); err != nil {
		return err
	}

	// Set as global
	if err := rt.SetGlobal("__dns_internal", dnsInternal); err != nil {
		return err
	}

	// Initialize JS wrapper
	if _, err := rt.RunScript(dnsJS, "dns.js"); err != nil {
		return err
	}

	return nil
}

func (d *DNS) createContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// lookupFunc implements dns.lookup
func (d *DNS) lookupFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	hostname := args[0].String()
	ctx := info.Context()
	resolver := d.rt.DNSResolver()

	lookupCtx, cancel := d.createContext()
	defer cancel()

	addrs, err := resolver.LookupHost(lookupCtx, hostname)
	if err != nil || len(addrs) == 0 {
		return nil
	}

	// Return first address
	val, _ := ctx.NewString(addrs[0])
	return val
}

// resolveFunc implements dns.resolve
func (d *DNS) resolveFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	hostname := args[0].String()
	rrtype := "A"
	if len(args) >= 2 && args[1].IsString() {
		rrtype = strings.ToUpper(args[1].String())
	}

	ctx := info.Context()
	resolver := d.rt.DNSResolver()

	lookupCtx, cancel := d.createContext()
	defer cancel()

	var result *v8go.Value

	switch rrtype {
	case "A":
		ips, err := resolver.LookupIP(lookupCtx, "ip4", hostname)
		if err != nil {
			return nil
		}
		arr, _ := ctx.NewArray(len(ips))
		for i, ip := range ips {
			val, _ := ctx.NewString(ip.String())
			arr.SetIndex(i, val)
		}
		result = arr
	case "AAAA":
		ips, err := resolver.LookupIP(lookupCtx, "ip6", hostname)
		if err != nil {
			return nil
		}
		arr, _ := ctx.NewArray(len(ips))
		for i, ip := range ips {
			val, _ := ctx.NewString(ip.String())
			arr.SetIndex(i, val)
		}
		result = arr
	case "MX":
		mxs, err := resolver.LookupMX(lookupCtx, hostname)
		if err != nil {
			return nil
		}
		arr, _ := ctx.NewArray(len(mxs))
		for i, mx := range mxs {
			obj, _ := ctx.NewObject()
			exchangeVal, _ := ctx.NewString(mx.Host)
			obj.Set("exchange", exchangeVal)
			obj.Set("priority", ctx.NewNumber(float64(mx.Pref)))
			arr.SetIndex(i, obj)
		}
		result = arr
	case "TXT":
		txts, err := resolver.LookupTXT(lookupCtx, hostname)
		if err != nil {
			return nil
		}
		arr, _ := ctx.NewArray(len(txts))
		for i, txt := range txts {
			// TXT records are arrays of strings
			innerArr, _ := ctx.NewArray(1)
			val, _ := ctx.NewString(txt)
			innerArr.SetIndex(0, val)
			arr.SetIndex(i, innerArr)
		}
		result = arr
	case "NS":
		nss, err := resolver.LookupNS(lookupCtx, hostname)
		if err != nil {
			return nil
		}
		arr, _ := ctx.NewArray(len(nss))
		for i, ns := range nss {
			val, _ := ctx.NewString(ns.Host)
			arr.SetIndex(i, val)
		}
		result = arr
	case "CNAME":
		cname, err := resolver.LookupCNAME(lookupCtx, hostname)
		if err != nil {
			return nil
		}
		arr, _ := ctx.NewArray(1)
		val, _ := ctx.NewString(cname)
		arr.SetIndex(0, val)
		result = arr
	default:
		return nil
	}

	return result
}

// resolve4Func implements dns.resolve4
func (d *DNS) resolve4Func(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	hostname := args[0].String()
	ctx := info.Context()
	resolver := d.rt.DNSResolver()

	lookupCtx, cancel := d.createContext()
	defer cancel()

	ips, err := resolver.LookupIP(lookupCtx, "ip4", hostname)
	if err != nil {
		return nil
	}

	arr, _ := ctx.NewArray(len(ips))
	for i, ip := range ips {
		val, _ := ctx.NewString(ip.String())
		arr.SetIndex(i, val)
	}
	return arr
}

// resolve6Func implements dns.resolve6
func (d *DNS) resolve6Func(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	hostname := args[0].String()
	ctx := info.Context()
	resolver := d.rt.DNSResolver()

	lookupCtx, cancel := d.createContext()
	defer cancel()

	ips, err := resolver.LookupIP(lookupCtx, "ip6", hostname)
	if err != nil {
		return nil
	}

	arr, _ := ctx.NewArray(len(ips))
	for i, ip := range ips {
		val, _ := ctx.NewString(ip.String())
		arr.SetIndex(i, val)
	}
	return arr
}

// resolveMxFunc implements dns.resolveMx
func (d *DNS) resolveMxFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	hostname := args[0].String()
	ctx := info.Context()
	resolver := d.rt.DNSResolver()

	lookupCtx, cancel := d.createContext()
	defer cancel()

	mxs, err := resolver.LookupMX(lookupCtx, hostname)
	if err != nil {
		return nil
	}

	arr, _ := ctx.NewArray(len(mxs))
	for i, mx := range mxs {
		obj, _ := ctx.NewObject()
		exchangeVal, _ := ctx.NewString(mx.Host)
		obj.Set("exchange", exchangeVal)
		obj.Set("priority", ctx.NewNumber(float64(mx.Pref)))
		arr.SetIndex(i, obj)
	}
	return arr
}

// resolveTxtFunc implements dns.resolveTxt
func (d *DNS) resolveTxtFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	hostname := args[0].String()
	ctx := info.Context()
	resolver := d.rt.DNSResolver()

	lookupCtx, cancel := d.createContext()
	defer cancel()

	txts, err := resolver.LookupTXT(lookupCtx, hostname)
	if err != nil {
		return nil
	}

	arr, _ := ctx.NewArray(len(txts))
	for i, txt := range txts {
		innerArr, _ := ctx.NewArray(1)
		val, _ := ctx.NewString(txt)
		innerArr.SetIndex(0, val)
		arr.SetIndex(i, innerArr)
	}
	return arr
}

// resolveNsFunc implements dns.resolveNs
func (d *DNS) resolveNsFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	hostname := args[0].String()
	ctx := info.Context()
	resolver := d.rt.DNSResolver()

	lookupCtx, cancel := d.createContext()
	defer cancel()

	nss, err := resolver.LookupNS(lookupCtx, hostname)
	if err != nil {
		return nil
	}

	arr, _ := ctx.NewArray(len(nss))
	for i, ns := range nss {
		val, _ := ctx.NewString(ns.Host)
		arr.SetIndex(i, val)
	}
	return arr
}

// resolveCnameFunc implements dns.resolveCname
func (d *DNS) resolveCnameFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	hostname := args[0].String()
	ctx := info.Context()
	resolver := d.rt.DNSResolver()

	lookupCtx, cancel := d.createContext()
	defer cancel()

	cname, err := resolver.LookupCNAME(lookupCtx, hostname)
	if err != nil {
		return nil
	}

	arr, _ := ctx.NewArray(1)
	val, _ := ctx.NewString(cname)
	arr.SetIndex(0, val)
	return arr
}

// reverseFunc implements dns.reverse
func (d *DNS) reverseFunc(info *v8go.FunctionCallbackInfo) *v8go.Value {
	args := info.Args()
	if len(args) < 1 {
		return nil
	}

	ip := args[0].String()
	ctx := info.Context()
	resolver := d.rt.DNSResolver()

	lookupCtx, cancel := d.createContext()
	defer cancel()

	names, err := resolver.LookupAddr(lookupCtx, ip)
	if err != nil {
		return nil
	}

	arr, _ := ctx.NewArray(len(names))
	for i, name := range names {
		val, _ := ctx.NewString(name)
		arr.SetIndex(i, val)
	}
	return arr
}
