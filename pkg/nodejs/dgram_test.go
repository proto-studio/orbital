package nodejs

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"

	"proto.zip/studio/orbital/pkg/runtime"
)

// fakeUDPFactory injects a UDP socket whose Send result the test controls.
type fakeUDPFactory struct {
	runtime.NoOpSocketFactory
	err   error
	mu    sync.Mutex
	sends []string
}

func (f *fakeUDPFactory) CreateUDPSocket(string) runtime.UDPSocket {
	return &fakeUDPSocket{factory: f}
}

type fakeUDPSocket struct {
	runtime.NoOpUDPSocket
	factory *fakeUDPFactory
}

func (s *fakeUDPSocket) Bind(context.Context, string, int) error { return nil }

func (s *fakeUDPSocket) Send(data []byte, address string, port int) (int, error) {
	s.factory.mu.Lock()
	defer s.factory.mu.Unlock()
	s.factory.sends = append(s.factory.sends, fmt.Sprintf("%s:%d:%x", address, port, data))
	if s.factory.err != nil {
		return 0, s.factory.err
	}
	return len(data), nil
}

func (s *fakeUDPSocket) LocalAddr() net.Addr { return nil }

const sendFormsJS = `
const dgram = require('dgram');
const socket = dgram.createSocket('udp4');
const forms = {
  fourArgs: (cb) => socket.send('hello', 4000, '192.0.2.1', cb),
  threeArgs: (cb) => socket.send('hello', 4000, cb),
  sixArgs: (cb) => socket.send(Buffer.from('xhellox'), 1, 5, 4000, '192.0.2.1', cb),
  fiveArgs: (cb) => socket.send(Buffer.from('xhellox'), 1, 5, 4000, cb),
  arrayFourArgs: (cb) => socket.send([Buffer.from('he'), 'llo'], 4000, '192.0.2.1', cb),
  stringSixArgs: (cb) => socket.send('xhellox', 1, 5, 4000, '192.0.2.1', cb),
};
const out = {};
socket.on('error', (e) => { out.emittedError = errInfo(e); });
(async () => {
  for (const name of Object.keys(forms)) {
    out[name] = { calls: 0 };
    await new Promise((resolve) => {
      forms[name]((err, bytes) => {
        out[name].calls++;
        out[name].error = errInfo(err);
        out[name].bytes = bytes;
        setTimeout(resolve, 5);
      });
    });
  }
  socket.close();
  done(out);
})();
`

func TestUDPSendCallbackFormsOnSuccess(t *testing.T) {
	factory := &fakeUDPFactory{}
	result := runJS(t, &runtime.Config{SocketFactory: factory}, sendFormsJS)
	for _, form := range []string{"fourArgs", "threeArgs", "sixArgs", "fiveArgs", "arrayFourArgs", "stringSixArgs"} {
		got := result[form].(map[string]any)
		if got["calls"] != float64(1) || got["error"] != nil || got["bytes"] != float64(5) {
			t.Errorf("%s: %v", form, got)
		}
	}
	if result["emittedError"] != nil {
		t.Errorf("unexpected error event: %v", result["emittedError"])
	}
	const hello = "68656c6c6f"
	want := []string{"192.0.2.1:4000:" + hello, "127.0.0.1:4000:" + hello, "192.0.2.1:4000:" + hello, "127.0.0.1:4000:" + hello, "192.0.2.1:4000:" + hello, "192.0.2.1:4000:" + hello}
	if fmt.Sprint(factory.sends) != fmt.Sprint(want) {
		t.Errorf("sends = %v, want %v", factory.sends, want)
	}
}

func TestUDPSendCallbackFormsOnInjectedError(t *testing.T) {
	factory := &fakeUDPFactory{err: errors.New("EACCES: denied by injected socket")}
	result := runJS(t, &runtime.Config{SocketFactory: factory}, sendFormsJS)
	for _, form := range []string{"fourArgs", "threeArgs", "sixArgs", "fiveArgs", "arrayFourArgs", "stringSixArgs"} {
		got := result[form].(map[string]any)
		info, _ := got["error"].(map[string]any)
		if got["calls"] != float64(1) || info == nil || info["code"] != "EACCES" || info["message"] != factory.err.Error() {
			t.Errorf("%s: %v", form, got)
		}
	}
	// With a callback the error goes there, not to an 'error' event.
	if result["emittedError"] != nil {
		t.Errorf("unexpected error event: %v", result["emittedError"])
	}
}

func TestUDPSendWithoutCallbackEmitsError(t *testing.T) {
	factory := &fakeUDPFactory{err: errors.New("EACCES: denied")}
	result := runJS(t, &runtime.Config{SocketFactory: factory}, `
const socket = require('dgram').createSocket('udp4');
socket.on('error', (e) => { socket.close(); done({ error: errInfo(e) }); });
socket.send('hello', 4000, '192.0.2.1');
`)
	if info, _ := result["error"].(map[string]any); info == nil || info["code"] != "EACCES" {
		t.Fatalf("result = %v", result)
	}
}

func TestUDPRealSocketRoundTrip(t *testing.T) {
	result := runJS(t, &runtime.Config{}, `
const dgram = require('dgram');
const server = dgram.createSocket('udp4');
server.on('message', (msg, rinfo) => server.send(msg, rinfo.port, rinfo.address));
server.bind(0, '127.0.0.1', () => {
  const port = server.address().port;
  // An unbound socket binds on first send, and receives replies, as in Node.
  const client = dgram.createSocket('udp4');
  const out = { calls: 0 };
  client.on('message', (msg) => {
    out.reply = [...msg];
    client.close();
    server.close();
    done(out);
  });
  client.send(Buffer.from([0, 255, 128, 0]), port, '127.0.0.1', (err, bytes) => {
    out.calls++;
    out.error = errInfo(err);
    out.bytes = bytes;
  });
});
`)
	if result["calls"] != float64(1) || result["error"] != nil || result["bytes"] != float64(4) {
		t.Fatalf("result = %v", result)
	}
	if fmt.Sprint(result["reply"]) != "[0 255 128 0]" {
		t.Fatalf("reply = %v", result["reply"])
	}
}

func TestUDPDenyPolicyReachesCallback(t *testing.T) {
	result := runJS(t, &runtime.Config{
		SocketFactory: runtime.NewFilteredSocketFactory(nil, runtime.NewDenyAllPolicy()),
	}, `
const socket = require('dgram').createSocket('udp4');
socket.send('hello', 4000, '127.0.0.1', (err) => { socket.close(); done({ error: errInfo(err) }); });
`)
	if info, _ := result["error"].(map[string]any); info == nil || info["code"] != "EACCES" {
		t.Fatalf("result = %v", result)
	}
}

// Binding on first send is subject to the same policy as an explicit bind.
func TestUDPImplicitBindHonorsInboundPolicy(t *testing.T) {
	policy := runtime.NewRuleBasedPolicy()
	policy.AddRule(&runtime.NetworkRule{Action: runtime.ActionAllow, Protocol: runtime.ProtocolUDP, Direction: runtime.DirectionOutbound})
	result := runJS(t, &runtime.Config{SocketFactory: runtime.NewFilteredSocketFactory(nil, policy)}, `
const socket = require('dgram').createSocket('udp4');
socket.send('hello', 4000, '127.0.0.1', (err) => { socket.close(); done({ error: errInfo(err) }); });
`)
	if info, _ := result["error"].(map[string]any); info == nil || info["code"] != "EACCES" {
		t.Fatalf("result = %v", result)
	}
}
