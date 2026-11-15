package nodejs

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"syscall"
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

// eyeballsFactory dials addresses in hang until the attempt is given up on, and the
// address in reachable to a local server. It records each (hostname, ip) it is asked for.
type eyeballsFactory struct {
	runtime.RealSocketFactory
	hang      map[string]bool
	reachable string
	target    string
	mu        sync.Mutex
	dials     []string
}

func (f *eyeballsFactory) CreateTCPSocket() runtime.TCPSocket {
	return &eyeballsSocket{RealTCPSocket: &runtime.RealTCPSocket{}, factory: f}
}

type eyeballsSocket struct {
	*runtime.RealTCPSocket
	factory *eyeballsFactory
}

func (s *eyeballsSocket) ConnectResolved(ctx context.Context, hostname, ip string, port int) error {
	s.factory.mu.Lock()
	s.factory.dials = append(s.factory.dials, hostname+"@"+ip)
	s.factory.mu.Unlock()
	if s.factory.hang[ip] {
		<-ctx.Done()
		return ctx.Err()
	}
	if ip == s.factory.reachable {
		host, port, _ := net.SplitHostPort(s.factory.target)
		return s.RealTCPSocket.Connect(ctx, host, atoi(port))
	}
	return &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
}

func (s *eyeballsSocket) Connect(ctx context.Context, address string, port int) error {
	return s.ConnectResolved(ctx, "", address, port)
}

// denyIP refuses connections to one address.
type denyIP struct {
	runtime.AllowAllPolicy
	ip string
}

func (p *denyIP) CheckConnection(protocol runtime.Protocol, direction runtime.Direction, host string, port int) error {
	if host == p.ip {
		return errors.New("EACCES: denied")
	}
	return nil
}

const eyeballsScript = `
const net = require('net');
const log = [];
const t0 = Date.now();
const s = net.connect({ host: 'dual.test', port: 443 %s });
for (const ev of ['lookup', 'connectionAttempt', 'connectionAttemptFailed', 'connectionAttemptTimeout']) {
  s.on(ev, (...args) => log.push([ev, ...args.map((a) => a instanceof Error ? a.code : a)].join(' ')));
}
s.on('connect', () => { log.push('connect'); s.destroy(); });
s.on('error', (e) => log.push('error ' + e.name + ' ' + e.code + ' ' + (e.errors || []).map((x) => x.code).join(',')));
s.on('close', () => done({ log, attempted: s.autoSelectFamilyAttemptedAddresses || null, ms: Date.now() - t0 }));
`

// A name with an unreachable IPv6 address and a reachable IPv4 one connects after the
// attempt timeout, through the injected resolver, factory and policy, as Node 20 does.
func TestNet_HappyEyeballs(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	resolver := &recordingResolver{hosts: map[string][]string{"dual.test": {"2001:db8::1", "192.0.2.10"}}}
	newFactory := func() *eyeballsFactory {
		return &eyeballsFactory{hang: map[string]bool{"2001:db8::1": true}, reachable: "192.0.2.10", target: ln.Addr().String()}
	}

	t.Run("falls back after the attempt timeout", func(t *testing.T) {
		factory := newFactory()
		got := runJS(t, &runtime.Config{DNSResolver: resolver, SocketFactory: factory}, fmt.Sprintf(eyeballsScript, ""))
		wantJSON(t, map[string]any{"log": got["log"], "attempted": got["attempted"]}, map[string]any{
			"log": []any{
				"lookup  2001:db8::1 6 dual.test",
				"lookup  192.0.2.10 4 dual.test",
				"connectionAttempt 2001:db8::1 443 6",
				"connectionAttemptTimeout 2001:db8::1 443 6",
				"connectionAttempt 192.0.2.10 443 4",
				"connect",
			},
			"attempted": []any{"2001:db8::1:443", "192.0.2.10:443"},
		})
		if ms := got["ms"].(float64); ms < 240 || ms > 5000 {
			t.Errorf("connected after %v ms", ms)
		}
		factory.mu.Lock()
		defer factory.mu.Unlock()
		if want := []string{"dual.test@2001:db8::1", "dual.test@192.0.2.10"}; fmt.Sprint(factory.dials) != fmt.Sprint(want) {
			t.Errorf("dials = %v, want %v", factory.dials, want)
		}
	})

	t.Run("the attempt timeout is an option", func(t *testing.T) {
		got := runJS(t, &runtime.Config{DNSResolver: resolver, SocketFactory: newFactory()}, fmt.Sprintf(eyeballsScript, ", autoSelectFamilyAttemptTimeout: 20"))
		if ms := got["ms"].(float64); ms > 200 {
			t.Errorf("connected after %v ms", ms)
		}
	})

	t.Run("without autoSelectFamily only the first address is tried", func(t *testing.T) {
		factory := newFactory()
		factory.hang = nil
		got := runJS(t, &runtime.Config{DNSResolver: resolver, SocketFactory: factory}, fmt.Sprintf(eyeballsScript, ", autoSelectFamily: false"))
		wantJSON(t, map[string]any{"log": got["log"], "attempted": got["attempted"]}, map[string]any{
			"log": []any{
				"lookup  2001:db8::1 6 dual.test",
				"connectionAttempt 2001:db8::1 443 6",
				"connectionAttemptFailed 2001:db8::1 443 6 ECONNREFUSED",
				"error Error ECONNREFUSED ",
			},
			"attempted": nil,
		})
	})

	t.Run("each address is still subject to the policy", func(t *testing.T) {
		factory := newFactory()
		filtered := runtime.NewFilteredSocketFactory(factory, &denyIP{ip: "192.0.2.10"})
		got := runJS(t, &runtime.Config{DNSResolver: resolver, SocketFactory: filtered}, fmt.Sprintf(eyeballsScript, ""))
		wantJSON(t, map[string]any{"log": got["log"]}, map[string]any{
			"log": []any{
				"lookup  2001:db8::1 6 dual.test",
				"lookup  192.0.2.10 4 dual.test",
				"connectionAttempt 2001:db8::1 443 6",
				"connectionAttemptTimeout 2001:db8::1 443 6",
				"connectionAttempt 192.0.2.10 443 4",
				"connectionAttemptFailed 192.0.2.10 443 4 EACCES",
				"error AggregateError ETIMEDOUT ETIMEDOUT,EACCES",
			},
		})
	})
}

// An address literal reports its one attempt, after connect() returns, as Node does.
func TestNet_ConnectionAttemptEvents(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	got := runJS(t, &runtime.Config{}, fmt.Sprintf(`
const net = require('net');
const log = [];
const s = net.connect(%d, '127.0.0.1');
s.on('connectionAttempt', (ip, port, family) => log.push(['attempt', ip, family].join(' ')));
s.on('connectionAttemptFailed', (ip, port, family, e) => log.push(['failed', ip, family, e.code].join(' ')));
s.on('error', (e) => log.push('error ' + e.code + ' ' + e.message.replace(String(%d), 'PORT')));
s.on('close', () => done({ log }));`, port, port))
	wantJSON(t, got, map[string]any{"log": []any{
		"attempt 127.0.0.1 4",
		"failed 127.0.0.1 4 ECONNREFUSED",
		"error ECONNREFUSED connect ECONNREFUSED 127.0.0.1:PORT",
	}})
}
